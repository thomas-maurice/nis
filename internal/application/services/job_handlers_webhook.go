package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/thomas-maurice/nis/internal/application/events"
	"github.com/thomas-maurice/nis/internal/clock"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/encryption"
	"github.com/thomas-maurice/nis/internal/infrastructure/logging"
	"github.com/thomas-maurice/nis/internal/infrastructure/metrics"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
	"github.com/thomas-maurice/nis/pkg/webhooks"
)

// JobTypeWebhookDeliver is the registered job type. One row per delivery.
const JobTypeWebhookDeliver = "webhook.deliver"

// WebhookHandlerConfig configures the webhook.deliver handler.
//
// MaxAttempts is mirrored both onto HandlerSpec.MaxAttempts (so the substrate
// dead-letters the *job* on the last failure) AND read inside the handler so
// it can dead-letter the *delivery row* on the same final attempt. Without
// the handler-side check, the typed row would be left in `failed` when the
// substrate gives up and the UI would show a stuck non-terminal state.
type WebhookHandlerConfig struct {
	MaxAttempts     int           // default 5
	BackoffBase     time.Duration // default 10s
	BackoffCap      time.Duration // default 10min
	DeliveryTimeout time.Duration // per-POST HTTP timeout; default 10s
}

func (c *WebhookHandlerConfig) applyDefaults() {
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = 5
	}
	if c.BackoffBase <= 0 {
		c.BackoffBase = 10 * time.Second
	}
	if c.BackoffCap <= 0 {
		c.BackoffCap = 10 * time.Minute
	}
	if c.DeliveryTimeout <= 0 {
		c.DeliveryTimeout = 10 * time.Second
	}
}

// webhookDeliverPayload is what the events fanout enqueues — just a pointer
// at the typed row. The handler re-reads the delivery on every claim so we
// never act on stale state (a sub disabled between enqueue and claim, etc).
type webhookDeliverPayload struct {
	DeliveryID uuid.UUID `json:"delivery_id"`
}

// webhookDeliverHandler is the per-delivery dispatcher.
type webhookDeliverHandler struct {
	factory     persistence.RepositoryFactory
	encryptor   encryption.Encryptor
	httpClient  *http.Client
	maxAttempts int
}

// RegisterWebhookHandlers wires the webhook.deliver handler onto the runner
// and installs the events package's job-enqueue callback so subsequent
// EmitTx calls atomically enqueue companion jobs alongside the delivery row.
//
// MUST be called before runner.Run starts (Register panics otherwise).
//
// On startup it also enqueues catch-up jobs for any webhook_deliveries rows
// already in `pending`/`failed` status without a live job — covers two cases:
// (1) operators upgrading from the pre-A16 worker who have rows mid-flight;
// (2) rare race where SetJobEnqueuer hadn't been called yet when an early
// EmitTx ran.
func RegisterWebhookHandlers(runner *JobRunner, factory persistence.RepositoryFactory, enc encryption.Encryptor, cfg WebhookHandlerConfig) {
	cfg.applyDefaults()

	h := &webhookDeliverHandler{
		factory:     factory,
		encryptor:   enc,
		httpClient:  &http.Client{Timeout: cfg.DeliveryTimeout},
		maxAttempts: cfg.MaxAttempts,
	}

	runner.Register(JobTypeWebhookDeliver, HandlerSpec{
		Handler:     h.Run,
		MaxAttempts: cfg.MaxAttempts,
		BackoffBase: cfg.BackoffBase,
		BackoffCap:  cfg.BackoffCap,
		AuditPolicy: AuditNone,
	})

	// Install the events → jobs bridge. Callback fires inside EmitTx's tx
	// alongside the webhook_deliveries row, so the two writes commit atomically.
	events.SetJobEnqueuer(newWebhookEnqueueFn(cfg.MaxAttempts))
}

// EnqueueCatchUpDeliveries enqueues a webhook.deliver job for every
// non-terminal delivery row that doesn't already have one. Idempotent — uses
// EnqueueIfAbsent so live jobs aren't double-enqueued.
//
// Returns the number of jobs ACTUALLY inserted (excluding rows that already
// had a live job). Errors enqueuing a single row are logged and skipped; the
// next call (or a manual retry) recovers.
func EnqueueCatchUpDeliveries(ctx context.Context, factory persistence.RepositoryFactory, maxAttempts int) (int, error) {
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	log := logging.LogFromContext(ctx)

	var enqueued int
	for _, status := range []entities.DeliveryStatus{entities.DeliveryStatusPending, entities.DeliveryStatusFailed} {
		page := 0
		for {
			filter := repositories.WebhookDeliveryFilter{Status: &status, Limit: 200, Offset: page * 200}
			rows, err := factory.WebhookDeliveryRepository().List(ctx, filter)
			if err != nil {
				return enqueued, fmt.Errorf("list non-terminal deliveries (%s): %w", status, err)
			}
			if len(rows) == 0 {
				break
			}
			for _, d := range rows {
				var inserted bool
				err := factory.WithTx(ctx, func(tx persistence.RepositoryFactory) error {
					j, err := buildWebhookDeliverJob(d.ID, maxAttempts)
					if err != nil {
						return err
					}
					ok, err := tx.JobRepository().EnqueueIfAbsent(ctx, j)
					inserted = ok
					return err
				})
				if err != nil {
					log.Warn("webhook catch-up: enqueue failed", "delivery_id", d.ID, "error", err)
					continue
				}
				if inserted {
					enqueued++
					metrics.Default().RecordJobEnqueued(JobTypeWebhookDeliver)
				}
			}
			if len(rows) < 200 {
				break
			}
			page++
		}
	}
	if enqueued > 0 {
		log.Info("webhook catch-up: enqueued orphan deliveries", "count", enqueued)
	}
	return enqueued, nil
}

// newWebhookEnqueueFn returns an events.JobEnqueueFn that inserts a
// webhook.deliver job row inside the caller's tx. dedup_key=delivery_id
// guarantees we never enqueue twice for the same delivery while one is
// already pending/running — duplicate inserts are swallowed silently
// (the catch-up scan is the recovery path, this is the live path).
func newWebhookEnqueueFn(maxAttempts int) events.JobEnqueueFn {
	return func(ctx context.Context, tx persistence.RepositoryFactory, deliveryID uuid.UUID) error {
		j, err := buildWebhookDeliverJob(deliveryID, maxAttempts)
		if err != nil {
			return err
		}
		if err := tx.JobRepository().Enqueue(ctx, j); err != nil {
			if errors.Is(err, repositories.ErrJobAlreadyEnqueued) {
				return nil
			}
			return err
		}
		metrics.Default().RecordJobEnqueued(JobTypeWebhookDeliver)
		return nil
	}
}

// buildWebhookDeliverJob constructs a Job entity ready for insert. Extracted
// so the live-enqueue and catch-up paths agree on field shape (especially
// dedup_key, which the partial unique index relies on).
func buildWebhookDeliverJob(deliveryID uuid.UUID, maxAttempts int) (*entities.Job, error) {
	body, err := json.Marshal(webhookDeliverPayload{DeliveryID: deliveryID})
	if err != nil {
		return nil, fmt.Errorf("marshal webhook deliver payload: %w", err)
	}
	now := clock.Now()
	return &entities.Job{
		ID:           uuid.New(),
		Type:         JobTypeWebhookDeliver,
		Payload:      body,
		Status:       entities.JobStatusPending,
		ScheduledFor: now,
		MaxAttempts:  maxAttempts,
		DedupKey:     deliveryID.String(),
		CreatedAt:    now,
		UpdatedAt:    now,
	}, nil
}

// Run is the JobHandler. Returns nil for terminal success/no-op, an err
// wrapping ErrPermanentJobFailure for permanent failures (no retry), or a
// plain err for transient failures (substrate retries with backoff).
func (h *webhookDeliverHandler) Run(ctx context.Context, payload []byte) error {
	var p webhookDeliverPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("unmarshal payload: %w: %w", err, ErrPermanentJobFailure)
	}
	if p.DeliveryID == uuid.Nil {
		return fmt.Errorf("payload missing delivery_id: %w", ErrPermanentJobFailure)
	}

	log := logging.LogFromContext(ctx)

	d, err := h.factory.WebhookDeliveryRepository().GetByID(ctx, p.DeliveryID)
	if err != nil {
		if errors.Is(err, repositories.ErrNotFound) {
			// Delivery cascade-deleted with its subscription. Job has nothing
			// to do — treat as success so the substrate doesn't keep retrying.
			log.Info("webhook deliver: delivery row gone", "delivery_id", p.DeliveryID)
			return nil
		}
		return fmt.Errorf("load delivery: %w", err)
	}

	// Idempotent re-claim guard. If the delivery is already terminal, an
	// earlier attempt succeeded but the substrate redelivered before the
	// MarkSucceeded write landed — never POST twice.
	if d.Status == entities.DeliveryStatusSucceeded || d.Status == entities.DeliveryStatusDeadLetter {
		return nil
	}

	return h.process(ctx, d)
}

func (h *webhookDeliverHandler) process(ctx context.Context, d *entities.WebhookDelivery) error {
	log := logging.LogFromContext(ctx)

	sub, err := h.factory.WebhookSubscriptionRepository().GetByID(ctx, d.SubscriptionID)
	if err != nil {
		log.Error("webhook deliver: load subscription", "delivery_id", d.ID, "error", err)
		return h.permanent(ctx, d, "subscription_load_error: "+err.Error(), 0)
	}

	// Subscription disabled between enqueue and dispatch: surface as
	// dead-letter so the delivery log shows it, rather than silently retrying
	// until MaxAttempts.
	if !sub.Enabled {
		return h.permanent(ctx, d, "subscription_disabled", 0)
	}

	evt, err := h.factory.EventRepository().GetByID(ctx, d.EventID)
	if err != nil {
		log.Error("webhook deliver: load event", "delivery_id", d.ID, "error", err)
		return h.permanent(ctx, d, "event_load_error: "+err.Error(), 0)
	}

	secretBytes, err := h.encryptor.Decrypt(ctx, sub.EncryptedSecret)
	if err != nil {
		// Unreadable secret: auto-disable the subscription and dead-letter the
		// delivery. Operator must fix the underlying key situation and
		// re-enable in the UI.
		log.Error("webhook deliver: decrypt secret", "subscription_id", sub.ID, "error", err)
		sub.Enabled = false
		sub.DisabledReason = "secret_unreadable"
		if upErr := h.factory.WebhookSubscriptionRepository().Update(ctx, sub); upErr != nil {
			log.Error("webhook deliver: disable subscription after decrypt failure", "subscription_id", sub.ID, "error", upErr)
		}
		return h.permanent(ctx, d, fmt.Sprintf("decrypt_failed: %s", err.Error()), 0)
	}

	body, err := marshalEventPayload(evt)
	if err != nil {
		log.Error("webhook deliver: marshal payload", "delivery_id", d.ID, "error", err)
		return h.permanent(ctx, d, "marshal_error: "+err.Error(), 0)
	}

	tsStr := strconv.FormatInt(clock.Now().Unix(), 10)
	sig := webhooks.Sign(secretBytes, tsStr, body)
	deliveryAttemptID := uuid.New().String()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.URL, bytes.NewReader(body))
	if err != nil {
		log.Error("webhook deliver: build request", "delivery_id", d.ID, "error", err)
		return h.permanent(ctx, d, "build_request_error: "+err.Error(), 0)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(webhooks.HeaderEvent, evt.Type)
	req.Header.Set(webhooks.HeaderDelivery, deliveryAttemptID)
	req.Header.Set(webhooks.HeaderSubscription, sub.ID.String())
	req.Header.Set(webhooks.HeaderTimestamp, tsStr)
	req.Header.Set(webhooks.HeaderSignature, sig)

	start := time.Now() // duration measurement only; tz-irrelevant
	resp, httpErr := h.httpClient.Do(req)
	elapsed := time.Since(start).Seconds()

	if httpErr != nil {
		return h.transient(ctx, d, httpErr.Error(), 0, elapsed)
	}
	_ = resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return h.success(ctx, d, resp.StatusCode, elapsed)
	}
	return h.transient(ctx, d, fmt.Sprintf("non-2xx response: %d", resp.StatusCode), resp.StatusCode, elapsed)
}

func (h *webhookDeliverHandler) success(ctx context.Context, d *entities.WebhookDelivery, code int, elapsed float64) error {
	now := clock.Now()
	d.Attempt++
	d.Status = entities.DeliveryStatusSucceeded
	d.LastResponseCode = code
	d.LastError = ""
	d.CompletedAt = &now
	d.UpdatedAt = now
	if err := h.factory.WebhookDeliveryRepository().Update(ctx, d); err != nil {
		// If we can't persist success, let the substrate retry. A duplicate
		// POST is preferable to losing the record entirely.
		return fmt.Errorf("persist delivery success: %w", err)
	}
	metrics.Default().RecordWebhookDelivery(ctx, "succeeded", elapsed)
	return nil
}

// permanent marks the delivery row as dead-lettered and returns a wrapped
// ErrPermanentJobFailure so the substrate also dead-letters the job (no
// retry). reason is recorded in delivery.last_error AND surfaced as the job's
// last_error string.
func (h *webhookDeliverHandler) permanent(ctx context.Context, d *entities.WebhookDelivery, reason string, code int) error {
	now := clock.Now()
	d.Status = entities.DeliveryStatusDeadLetter
	d.LastError = reason
	d.LastResponseCode = code
	d.CompletedAt = &now
	d.UpdatedAt = now
	if err := h.factory.WebhookDeliveryRepository().Update(ctx, d); err != nil {
		logging.LogFromContext(ctx).Error("webhook deliver: persist dead_letter", "delivery_id", d.ID, "error", err)
	}
	metrics.Default().RecordWebhookDelivery(ctx, "dead_letter", 0)
	return fmt.Errorf("%s: %w", reason, ErrPermanentJobFailure)
}

// transient records the failure on the typed row and returns:
//   - a wrapped ErrPermanentJobFailure when this was the final allowed attempt
//     (also flips delivery.status to dead_letter so the UI doesn't show a
//     stuck "failed");
//   - a plain error otherwise — substrate retries with its computed backoff.
func (h *webhookDeliverHandler) transient(ctx context.Context, d *entities.WebhookDelivery, reason string, code int, elapsed float64) error {
	d.Attempt++
	d.LastError = reason
	d.LastResponseCode = code
	d.UpdatedAt = clock.Now()

	if d.Attempt >= h.maxAttempts {
		now := clock.Now()
		d.Status = entities.DeliveryStatusDeadLetter
		d.CompletedAt = &now
		if err := h.factory.WebhookDeliveryRepository().Update(ctx, d); err != nil {
			logging.LogFromContext(ctx).Error("webhook deliver: persist final dead_letter", "delivery_id", d.ID, "error", err)
		}
		metrics.Default().RecordWebhookDelivery(ctx, "dead_letter", elapsed)
		return fmt.Errorf("%s: %w", reason, ErrPermanentJobFailure)
	}

	d.Status = entities.DeliveryStatusFailed
	if err := h.factory.WebhookDeliveryRepository().Update(ctx, d); err != nil {
		logging.LogFromContext(ctx).Error("webhook deliver: persist transient failure", "delivery_id", d.ID, "error", err)
	}
	metrics.Default().RecordWebhookDelivery(ctx, "failed", elapsed)
	return errors.New(reason)
}

// eventPayload is the JSON shape delivered to webhook receivers. Format
// preserved from the pre-A16 worker — receivers depend on it.
type eventPayload struct {
	ID           string          `json:"id"`
	Type         string          `json:"type"`
	OccurredAt   time.Time       `json:"occurred_at"`
	OperatorID   *string         `json:"operator_id,omitempty"`
	AccountID    *string         `json:"account_id,omitempty"`
	ResourceType string          `json:"resource_type"`
	ResourceID   string          `json:"resource_id"`
	ActorType    string          `json:"actor_type"`
	ActorID      *string         `json:"actor_id,omitempty"`
	Payload      json.RawMessage `json:"payload,omitempty"`
}

func marshalEventPayload(evt *entities.Event) ([]byte, error) {
	p := eventPayload{
		ID:           evt.ID.String(),
		Type:         evt.Type,
		OccurredAt:   evt.OccurredAt,
		ResourceType: evt.ResourceType,
		ResourceID:   evt.ResourceID,
		ActorType:    string(evt.ActorType),
		Payload:      evt.Payload,
	}
	if evt.OperatorID != nil {
		s := evt.OperatorID.String()
		p.OperatorID = &s
	}
	if evt.AccountID != nil {
		s := evt.AccountID.String()
		p.AccountID = &s
	}
	if evt.ActorID != nil {
		s := evt.ActorID.String()
		p.ActorID = &s
	}
	return json.Marshal(p)
}
