package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/infrastructure/encryption"
	"github.com/thomas-maurice/nis/internal/infrastructure/logging"
	"github.com/thomas-maurice/nis/internal/infrastructure/metrics"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
	"github.com/thomas-maurice/nis/pkg/webhooks"
)

// WebhookDeliveryWorkerConfig configures the delivery worker.
type WebhookDeliveryWorkerConfig struct {
	PollInterval    time.Duration // how often to call ClaimDue
	DeliveryTimeout time.Duration // per-POST HTTP timeout; default 10s
	MaxAttempts     int           // deliveries become dead_letter after this many failures; default 5
	BackoffBase     time.Duration // exponential-backoff base; default 10s
	BackoffCap      time.Duration // exponential-backoff cap; default 10min
	ShutdownTimeout time.Duration // graceful drain of in-flight deliveries; default 30s
	BatchSize       int           // deliveries claimed per tick; default 50
}

func (c *WebhookDeliveryWorkerConfig) applyDefaults() {
	if c.PollInterval == 0 {
		c.PollInterval = 2 * time.Second
	}
	if c.DeliveryTimeout == 0 {
		c.DeliveryTimeout = 10 * time.Second
	}
	if c.MaxAttempts == 0 {
		c.MaxAttempts = 5
	}
	if c.BackoffBase == 0 {
		c.BackoffBase = 10 * time.Second
	}
	if c.BackoffCap == 0 {
		c.BackoffCap = 10 * time.Minute
	}
	if c.ShutdownTimeout == 0 {
		c.ShutdownTimeout = 30 * time.Second
	}
	if c.BatchSize == 0 {
		c.BatchSize = 50
	}
}

// WebhookDeliveryWorker polls webhook_deliveries for due rows, posts them to
// receivers with HMAC-SHA256 signatures, and tracks retry state. Single
// instance per process — there is no leader election today and the dispatch
// model is single-replica.
//
// Side-effect rule: the HTTP POST happens OUTSIDE any tx. State writes for
// the delivery row run in their own short operation after the POST returns.
type WebhookDeliveryWorker struct {
	factory   persistence.RepositoryFactory
	encryptor encryption.Encryptor
	cfg       WebhookDeliveryWorkerConfig

	httpClient *http.Client
	inflight   sync.WaitGroup
}

// NewWebhookDeliveryWorker creates a ready-to-run delivery worker.
func NewWebhookDeliveryWorker(factory persistence.RepositoryFactory, enc encryption.Encryptor, cfg WebhookDeliveryWorkerConfig) *WebhookDeliveryWorker {
	cfg.applyDefaults()
	return &WebhookDeliveryWorker{
		factory:   factory,
		encryptor: enc,
		cfg:       cfg,
		httpClient: &http.Client{
			Timeout: cfg.DeliveryTimeout,
		},
	}
}

// Run blocks until ctx is cancelled. On cancel, it stops polling and waits up
// to ShutdownTimeout for in-flight deliveries to finish.
func (w *WebhookDeliveryWorker) Run(ctx context.Context) {
	log := logging.GetLogger()
	log.Info("webhook delivery worker started",
		"poll_interval", w.cfg.PollInterval,
		"max_attempts", w.cfg.MaxAttempts,
		"batch_size", w.cfg.BatchSize,
	)

	ticker := time.NewTicker(w.cfg.PollInterval)
	defer ticker.Stop()

	// errBackoff tracks how long to sleep after a ClaimDue error so we don't
	// spam logs during a DB outage.
	errBackoff := time.Second

	for {
		select {
		case <-ctx.Done():
			log.Info("webhook delivery worker stopping, draining in-flight deliveries")
			done := make(chan struct{})
			go func() {
				w.inflight.Wait()
				close(done)
			}()
			select {
			case <-done:
				log.Info("webhook delivery worker drained")
			case <-time.After(w.cfg.ShutdownTimeout):
				log.Warn("webhook delivery worker shutdown timed out; some deliveries may be re-attempted on next start")
			}
			return
		case <-ticker.C:
			due, err := w.factory.WebhookDeliveryRepository().ClaimDue(ctx, time.Now().UTC(), w.cfg.BatchSize)
			if err != nil {
				log.Error("webhook delivery worker: claim due failed", "error", err, "backoff", errBackoff)
				select {
				case <-ctx.Done():
					return
				case <-time.After(errBackoff):
				}
				if errBackoff < 30*time.Second {
					errBackoff *= 2
					if errBackoff > 30*time.Second {
						errBackoff = 30 * time.Second
					}
				}
				continue
			}
			errBackoff = time.Second // reset on success

			for _, d := range due {
				d := d
				w.inflight.Add(1)
				go func() {
					defer w.inflight.Done()
					w.processOne(ctx, d)
				}()
			}
		}
	}
}

// processOne handles a single delivery: loads the event and subscription,
// signs and POSTs the payload, then updates the delivery row.
// Exported for use in tests (same package — lower-cased OK too, but exporting
// avoids a test file in a separate package needing reflection).
func (w *WebhookDeliveryWorker) processOne(ctx context.Context, d *entities.WebhookDelivery) {
	log := logging.GetLogger()

	sub, err := w.factory.WebhookSubscriptionRepository().GetByID(ctx, d.SubscriptionID)
	if err != nil {
		log.Error("delivery worker: load subscription", "delivery_id", d.ID, "error", err)
		w.markDeadLetter(ctx, d, "subscription_load_error: "+err.Error(), 0)
		return
	}

	// Subscription disabled between enqueue and dispatch: dead-letter so it is
	// visible in the delivery log rather than silently retried until max attempts.
	if !sub.Enabled {
		w.markDeadLetter(ctx, d, "subscription_disabled", 0)
		return
	}

	evt, err := w.factory.EventRepository().GetByID(ctx, d.EventID)
	if err != nil {
		log.Error("delivery worker: load event", "delivery_id", d.ID, "error", err)
		w.markDeadLetter(ctx, d, "event_load_error: "+err.Error(), 0)
		return
	}

	secretBytes, err := w.encryptor.Decrypt(ctx, sub.EncryptedSecret)
	if err != nil {
		// Unreadable secret: auto-disable the subscription and dead-letter.
		log.Error("delivery worker: decrypt webhook secret", "subscription_id", sub.ID, "error", err)
		sub.Enabled = false
		sub.DisabledReason = "secret_unreadable"
		if updateErr := w.factory.WebhookSubscriptionRepository().Update(ctx, sub); updateErr != nil {
			log.Error("delivery worker: disable subscription after decrypt failure", "subscription_id", sub.ID, "error", updateErr)
		}
		w.markDeadLetter(ctx, d, fmt.Sprintf("decrypt_failed: %s", err.Error()), 0)
		return
	}

	body, err := marshalEventPayload(evt)
	if err != nil {
		log.Error("delivery worker: marshal event payload", "delivery_id", d.ID, "error", err)
		w.markDeadLetter(ctx, d, "marshal_error: "+err.Error(), 0)
		return
	}

	tsStr := strconv.FormatInt(time.Now().UTC().Unix(), 10)
	sig := webhooks.Sign(secretBytes, tsStr, body)
	deliveryAttemptID := uuid.New().String()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.URL, bytes.NewReader(body))
	if err != nil {
		log.Error("delivery worker: build request", "delivery_id", d.ID, "error", err)
		w.markDeadLetter(ctx, d, "build_request_error: "+err.Error(), 0)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(webhooks.HeaderEvent, evt.Type)
	req.Header.Set(webhooks.HeaderDelivery, deliveryAttemptID)
	req.Header.Set(webhooks.HeaderSubscription, sub.ID.String())
	req.Header.Set(webhooks.HeaderTimestamp, tsStr)
	req.Header.Set(webhooks.HeaderSignature, sig)

	start := time.Now()
	resp, err := w.httpClient.Do(req)
	elapsed := time.Since(start).Seconds()

	if err != nil {
		// Transport error (connection refused, timeout, etc.)
		d.Attempt++
		d.LastError = err.Error()
		d.LastResponseCode = 0
		d.UpdatedAt = time.Now().UTC()
		if d.Attempt >= w.cfg.MaxAttempts {
			now := time.Now().UTC()
			d.Status = entities.DeliveryStatusDeadLetter
			d.CompletedAt = &now
			metrics.Default().RecordWebhookDelivery(ctx, "dead_letter", elapsed)
		} else {
			d.Status = entities.DeliveryStatusPending
			d.NextAttemptAt = time.Now().UTC().Add(computeBackoff(d.Attempt, w.cfg.BackoffBase, w.cfg.BackoffCap))
			metrics.Default().RecordWebhookDelivery(ctx, "failed", elapsed)
		}
		if updateErr := w.factory.WebhookDeliveryRepository().Update(ctx, d); updateErr != nil {
			log.Error("delivery worker: update delivery after transport error", "delivery_id", d.ID, "error", updateErr)
		}
		return
	}
	_ = resp.Body.Close()

	statusCode := resp.StatusCode
	d.LastResponseCode = statusCode
	d.UpdatedAt = time.Now().UTC()

	if statusCode >= 200 && statusCode < 300 {
		now := time.Now().UTC()
		d.Status = entities.DeliveryStatusSucceeded
		d.CompletedAt = &now
		d.Attempt++
		metrics.Default().RecordWebhookDelivery(ctx, "succeeded", elapsed)
	} else {
		d.Attempt++
		d.LastError = fmt.Sprintf("non-2xx response: %d", statusCode)
		if d.Attempt >= w.cfg.MaxAttempts {
			now := time.Now().UTC()
			d.Status = entities.DeliveryStatusDeadLetter
			d.CompletedAt = &now
			metrics.Default().RecordWebhookDelivery(ctx, "dead_letter", elapsed)
		} else {
			d.Status = entities.DeliveryStatusPending
			d.NextAttemptAt = time.Now().UTC().Add(computeBackoff(d.Attempt, w.cfg.BackoffBase, w.cfg.BackoffCap))
			metrics.Default().RecordWebhookDelivery(ctx, "failed", elapsed)
		}
	}

	if updateErr := w.factory.WebhookDeliveryRepository().Update(ctx, d); updateErr != nil {
		log.Error("delivery worker: update delivery", "delivery_id", d.ID, "error", updateErr)
	}
}

func (w *WebhookDeliveryWorker) markDeadLetter(ctx context.Context, d *entities.WebhookDelivery, reason string, code int) {
	now := time.Now().UTC()
	d.Status = entities.DeliveryStatusDeadLetter
	d.LastError = reason
	d.LastResponseCode = code
	d.CompletedAt = &now
	d.UpdatedAt = now
	if err := w.factory.WebhookDeliveryRepository().Update(ctx, d); err != nil {
		logging.GetLogger().Error("delivery worker: persist dead-letter", "delivery_id", d.ID, "error", err)
	}
	metrics.Default().RecordWebhookDelivery(ctx, "dead_letter", 0)
}

// computeBackoff returns a jittered exponential backoff duration for the given
// attempt count (1-based). Jitter is in [0.8, 1.2] to spread retries.
func computeBackoff(attempt int, base, cap time.Duration) time.Duration {
	shift := attempt - 1
	if shift < 0 {
		shift = 0
	}
	// 2^shift capped to avoid overflow — max shift of 30 is plenty.
	if shift > 30 {
		shift = 30
	}
	d := base * (1 << uint(shift))
	if d <= 0 || d > cap {
		d = cap
	}
	// Jitter: multiply by a value in [0.8, 1.2].
	jitter := 0.8 + rand.Float64()*0.4
	d = time.Duration(float64(d) * jitter)
	if d > cap {
		d = cap
	}
	return d
}

// eventPayload is the JSON shape delivered to webhook receivers.
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
