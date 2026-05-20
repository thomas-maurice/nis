// Package metrics wires OpenTelemetry metrics into NIS. It uses the OTel
// Prometheus exporter so that scraping `/metrics` works the same way as any
// Prometheus-instrumented service, while internally we record into OTel
// instruments — which lets the same code emit traces (when tracing is enabled)
// without a second instrumentation path.
//
// Lifecycle:
//
//   p, handler, err := metrics.New("nis", version)   // wires global meter provider
//   defer p.Shutdown(ctx)
//   mux.Handle("/metrics", handler)
//
// Recorders surface frequently-used instruments as typed methods, so callers
// don't redeclare them at every recording site. See Recorder below.
package metrics

import (
	"context"
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
)

// scope is the instrumentation scope used for all NIS-emitted metrics.
const scope = "github.com/thomas-maurice/nis"

// defaultRecorder is a package-level recorder set by New(). Callers can use
// Default() from anywhere without threading a recorder through constructors.
// All Recorder methods are nil-safe, so unset (e.g. tests) is fine.
var defaultRecorder *Recorder

// Default returns the package-level recorder. Always safe to call: returns nil
// when metrics have not been initialised, and every Recorder method tolerates
// a nil receiver.
func Default() *Recorder {
	return defaultRecorder
}

// Provider owns the OTel meter provider, the Prometheus registry, and the
// shared Recorder. Shutdown flushes pending data and tears the SDK down.
type Provider struct {
	meterProvider *sdkmetric.MeterProvider
	registry      *prometheus.Registry
	Recorder      *Recorder
}

// New initialises a Prometheus-backed OTel meter provider, registers process /
// Go-runtime collectors, and returns the provider plus an HTTP handler suitable
// for `/metrics`. The global OTel meter provider is set, so any package can
// `otel.Meter(...)` and obtain a real meter — including connectrpc.com/otelconnect.
func New(serviceName, version string) (*Provider, http.Handler, error) {
	registry := prometheus.NewRegistry()
	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	exporter, err := otelprom.New(otelprom.WithRegisterer(registry))
	if err != nil {
		return nil, nil, fmt.Errorf("create prometheus exporter: %w", err)
	}

	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(serviceName),
		semconv.ServiceVersion(version),
	))
	if err != nil {
		return nil, nil, fmt.Errorf("build resource: %w", err)
	}

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(exporter),
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(mp)

	rec, err := newRecorder(mp.Meter(scope))
	if err != nil {
		_ = mp.Shutdown(context.Background())
		return nil, nil, fmt.Errorf("build recorder: %w", err)
	}

	p := &Provider{
		meterProvider: mp,
		registry:      registry,
		Recorder:      rec,
	}
	defaultRecorder = rec

	handler := promhttp.HandlerFor(registry, promhttp.HandlerOpts{
		Registry:          registry,
		EnableOpenMetrics: true,
	})

	return p, handler, nil
}

// Shutdown flushes the meter provider. Idempotent.
func (p *Provider) Shutdown(ctx context.Context) error {
	if p == nil || p.meterProvider == nil {
		return nil
	}
	return p.meterProvider.Shutdown(ctx)
}

// Registry exposes the underlying Prometheus registry. Useful for tests or for
// callers that want to register additional collectors (e.g. custom Go/CGo
// integrations) without going through OTel.
func (p *Provider) Registry() *prometheus.Registry {
	return p.registry
}

// Recorder bundles the instruments NIS records into from its own code (cluster
// sync, encryption, auth interceptor). RPC and HTTP metrics are emitted by
// otelconnect and the HTTP middleware respectively — see http_middleware.go.
type Recorder struct {
	clusterSyncErrors    metric.Int64Counter
	clusterSyncDuration  metric.Float64Histogram
	clusterHealthFailed  metric.Int64Counter
	encryptionFailures   metric.Int64Counter
	authRejections       metric.Int64Counter

	httpDuration metric.Float64Histogram

	webhookDeliveries        metric.Int64Counter
	webhookDeliveryDuration  metric.Float64Histogram
	eventsEmitted            metric.Int64Counter
	apiTokenAuthentications  metric.Int64Counter

	// P2 — JWT lifecycle.
	userJWTRevocations        metric.Int64Counter
	userJWTRevocationsPruned  metric.Int64Counter
	userJWTExpiringSoonEvents metric.Int64Counter
	userJWTExpiredEvents      metric.Int64Counter
	userJWTAutoRenewals       metric.Int64Counter

	// P9 — cluster drift dashboard.
	clusterDriftScans   metric.Int64Counter
	clusterDriftResults metric.Int64Counter

	// A2 — jobs substrate.
	jobsEnqueued  metric.Int64Counter
	jobsCompleted metric.Int64Counter
	jobDuration   metric.Float64Histogram
}

func newRecorder(m metric.Meter) (*Recorder, error) {
	var (
		r   Recorder
		err error
	)
	if r.clusterSyncErrors, err = m.Int64Counter(
		"nis_cluster_sync_errors_total",
		metric.WithDescription("Total number of errors encountered during cluster JWT sync, labelled by phase."),
	); err != nil {
		return nil, err
	}
	if r.clusterSyncDuration, err = m.Float64Histogram(
		"nis_cluster_sync_duration_seconds",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of cluster JWT sync operations, labelled by outcome (ok/err)."),
	); err != nil {
		return nil, err
	}
	if r.clusterHealthFailed, err = m.Int64Counter(
		"nis_cluster_health_check_failures_total",
		metric.WithDescription("Total number of cluster health checks that returned an error."),
	); err != nil {
		return nil, err
	}
	if r.encryptionFailures, err = m.Int64Counter(
		"nis_encryption_failures_total",
		metric.WithDescription("Encryption / decryption failures, labelled by op (encrypt/decrypt)."),
	); err != nil {
		return nil, err
	}
	if r.authRejections, err = m.Int64Counter(
		"nis_auth_rejections_total",
		metric.WithDescription("RPC requests rejected by the auth interceptor, labelled by reason."),
	); err != nil {
		return nil, err
	}
	if r.httpDuration, err = m.Float64Histogram(
		"nis_http_server_duration_seconds",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of non-RPC HTTP requests, labelled by path class, method, and status."),
	); err != nil {
		return nil, err
	}
	if r.webhookDeliveries, err = m.Int64Counter(
		"nis_webhook_deliveries_total",
		metric.WithDescription("Total webhook delivery attempts, labelled by status (succeeded/failed/dead_letter)."),
	); err != nil {
		return nil, err
	}
	if r.webhookDeliveryDuration, err = m.Float64Histogram(
		"nis_webhook_delivery_duration_seconds",
		metric.WithUnit("s"),
		metric.WithDescription("Time spent on a single webhook HTTP POST attempt."),
	); err != nil {
		return nil, err
	}
	if r.eventsEmitted, err = m.Int64Counter(
		"nis_events_emitted_total",
		metric.WithDescription("Total events emitted, labelled by type."),
	); err != nil {
		return nil, err
	}
	if r.apiTokenAuthentications, err = m.Int64Counter(
		"nis_api_token_authentications_total",
		metric.WithDescription("Total API token auth attempts, labelled by status (success/invalid/expired/revoked)."),
	); err != nil {
		return nil, err
	}
	if r.userJWTRevocations, err = m.Int64Counter(
		"nis_user_jwt_revocations_total",
		metric.WithDescription("Total user JWT revocations, labelled by outcome (ok/err)."),
	); err != nil {
		return nil, err
	}
	if r.userJWTRevocationsPruned, err = m.Int64Counter(
		"nis_user_jwt_revocations_pruned_total",
		metric.WithDescription("Total revocation entries pruned from account JWTs after the revoked JWT exp passed."),
	); err != nil {
		return nil, err
	}
	if r.userJWTExpiringSoonEvents, err = m.Int64Counter(
		"nis_user_jwt_expiring_soon_events_total",
		metric.WithDescription("Total user.cred.expiring_soon events emitted by the sweeper."),
	); err != nil {
		return nil, err
	}
	if r.userJWTExpiredEvents, err = m.Int64Counter(
		"nis_user_jwt_expired_events_total",
		metric.WithDescription("Total user.cred.expired events emitted by the sweeper (post-exp credentials)."),
	); err != nil {
		return nil, err
	}
	if r.userJWTAutoRenewals, err = m.Int64Counter(
		"nis_user_jwt_auto_renewals_total",
		metric.WithDescription("Total automatic user JWT renewals attempted by the sweeper, labelled by status (ok/err)."),
	); err != nil {
		return nil, err
	}
	if r.clusterDriftScans, err = m.Int64Counter(
		"nis_cluster_drift_scans_total",
		metric.WithDescription("Total cluster drift scans performed, labelled by outcome (ok/partial/error). 'partial' means the scan returned per-row results but at least one row was not in_sync."),
	); err != nil {
		return nil, err
	}
	if r.jobsEnqueued, err = m.Int64Counter(
		"nis_jobs_enqueued_total",
		metric.WithDescription("Total jobs enqueued, labelled by type. Includes EnsureScheduled inserts that won the watchdog race; does NOT count rows skipped because a pending/running row already existed."),
	); err != nil {
		return nil, err
	}
	if r.jobsCompleted, err = m.Int64Counter(
		"nis_jobs_completed_total",
		metric.WithDescription("Total jobs completed, labelled by type and outcome (succeeded/failed/dead_lettered). 'failed' is a transient failure followed by retry; 'dead_lettered' is terminal."),
	); err != nil {
		return nil, err
	}
	if r.jobDuration, err = m.Float64Histogram(
		"nis_job_duration_seconds",
		metric.WithUnit("s"),
		metric.WithDescription("Time spent executing a single job handler invocation, labelled by type. No worker_id label (cardinality footgun in containers)."),
	); err != nil {
		return nil, err
	}
	if r.clusterDriftResults, err = m.Int64Counter(
		"nis_cluster_drift_results_total",
		metric.WithDescription("Total per-account drift classifications produced by scans, labelled by status. No cluster_id/account_id labels — cardinality is bounded by the status enum."),
	); err != nil {
		return nil, err
	}
	return &r, nil
}

// RecordUserJWTRevocation records a user revocation outcome. status="ok"|"err".
func (r *Recorder) RecordUserJWTRevocation(ctx context.Context, status string) {
	if r == nil {
		return
	}
	r.userJWTRevocations.Add(ctx, 1, metric.WithAttributes(attribute.String("status", status)))
}

// RecordUserJWTRevocationPruned records the number of revocation entries
// pruned in a single sweep batch.
func (r *Recorder) RecordUserJWTRevocationPruned(ctx context.Context, count int) {
	if r == nil || count <= 0 {
		return
	}
	r.userJWTRevocationsPruned.Add(ctx, int64(count))
}

// RecordUserJWTExpiringSoonEvent increments the expiring-soon emission counter.
func (r *Recorder) RecordUserJWTExpiringSoonEvent(ctx context.Context) {
	if r == nil {
		return
	}
	r.userJWTExpiringSoonEvents.Add(ctx, 1)
}

// RecordUserJWTExpiredEvent increments the expired-alert emission counter.
func (r *Recorder) RecordUserJWTExpiredEvent(ctx context.Context) {
	if r == nil {
		return
	}
	r.userJWTExpiredEvents.Add(ctx, 1)
}

// RecordUserJWTAutoRenewal increments the auto-renewal counter. status="ok"|"err".
func (r *Recorder) RecordUserJWTAutoRenewal(ctx context.Context, status string) {
	if r == nil {
		return
	}
	r.userJWTAutoRenewals.Add(ctx, 1, metric.WithAttributes(attribute.String("status", status)))
}

// RecordClusterSyncError increments the sync-error counter for the given phase
// ("decrypt_creds", "connect", "push_account", "verify", ...).
func (r *Recorder) RecordClusterSyncError(ctx context.Context, phase string) {
	if r == nil {
		return
	}
	r.clusterSyncErrors.Add(ctx, 1, metric.WithAttributes(attribute.String("phase", phase)))
}

// RecordClusterSyncDuration records the time taken to sync a cluster.
func (r *Recorder) RecordClusterSyncDuration(ctx context.Context, seconds float64, outcome string) {
	if r == nil {
		return
	}
	r.clusterSyncDuration.Record(ctx, seconds, metric.WithAttributes(attribute.String("outcome", outcome)))
}

// RecordClusterHealthCheckFailure increments the cluster-health-check failure counter.
func (r *Recorder) RecordClusterHealthCheckFailure(ctx context.Context) {
	if r == nil {
		return
	}
	r.clusterHealthFailed.Add(ctx, 1)
}

// RecordEncryptionFailure increments the encryption failure counter. op is
// "encrypt" or "decrypt".
func (r *Recorder) RecordEncryptionFailure(ctx context.Context, op string) {
	if r == nil {
		return
	}
	r.encryptionFailures.Add(ctx, 1, metric.WithAttributes(attribute.String("op", op)))
}

// RecordAuthRejection increments the auth rejection counter. reason is one of
// "missing_token", "invalid_token", "forbidden".
func (r *Recorder) RecordAuthRejection(ctx context.Context, reason string) {
	if r == nil {
		return
	}
	r.authRejections.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", reason)))
}

// recordHTTPDuration is called from the HTTP middleware. Not exported; the
// middleware lives in the same package.
func (r *Recorder) recordHTTPDuration(ctx context.Context, seconds float64, pathClass, method string, status int) {
	if r == nil {
		return
	}
	r.httpDuration.Record(ctx, seconds, metric.WithAttributes(
		attribute.String("path_class", pathClass),
		attribute.String("method", method),
		attribute.Int("status", status),
	))
}

// RecordWebhookDelivery records the outcome of a single webhook delivery attempt.
// status is one of "succeeded", "failed", "dead_letter".
func (r *Recorder) RecordWebhookDelivery(ctx context.Context, status string, durationSeconds float64) {
	if r == nil {
		return
	}
	attrs := metric.WithAttributes(attribute.String("status", status))
	r.webhookDeliveries.Add(ctx, 1, attrs)
	r.webhookDeliveryDuration.Record(ctx, durationSeconds, attrs)
}

// RecordAPITokenAuthentication increments the API token auth counter.
// status is one of "success", "invalid", "expired", "revoked".
func (r *Recorder) RecordAPITokenAuthentication(ctx context.Context, status string) {
	if r == nil {
		return
	}
	r.apiTokenAuthentications.Add(ctx, 1, metric.WithAttributes(attribute.String("status", status)))
}

// RecordEventEmitted increments the events-emitted counter. eventType should be
// one of the EventType* constants from entities (e.g. "account.created").
func (r *Recorder) RecordEventEmitted(ctx context.Context, eventType string) {
	if r == nil {
		return
	}
	r.eventsEmitted.Add(ctx, 1, metric.WithAttributes(attribute.String("type", eventType)))
}

// RecordClusterDriftScan increments the per-scan outcome counter.
// outcome: "ok" (all rows in_sync), "partial" (some rows not in_sync or
// unreachable), "error" (scan failed before producing rows).
func (r *Recorder) RecordClusterDriftScan(ctx context.Context, outcome string) {
	if r == nil {
		return
	}
	r.clusterDriftScans.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", outcome)))
}

// RecordJobEnqueued increments the per-type enqueue counter. Called from
// JobRunner.Enqueue and EnsureScheduled (only on actual insert, not skip).
func (r *Recorder) RecordJobEnqueued(jobType string) {
	if r == nil {
		return
	}
	r.jobsEnqueued.Add(context.Background(), 1, metric.WithAttributes(attribute.String("type", jobType)))
}

// RecordJobCompleted increments the per-type, per-outcome completion
// counter. outcome is one of "succeeded", "failed", "dead_lettered".
func (r *Recorder) RecordJobCompleted(jobType, outcome string) {
	if r == nil {
		return
	}
	r.jobsCompleted.Add(context.Background(), 1, metric.WithAttributes(
		attribute.String("type", jobType),
		attribute.String("outcome", outcome),
	))
}

// RecordJobDuration records the time taken by one handler invocation,
// regardless of outcome (success or failure).
func (r *Recorder) RecordJobDuration(jobType string, seconds float64) {
	if r == nil {
		return
	}
	r.jobDuration.Record(context.Background(), seconds, metric.WithAttributes(attribute.String("type", jobType)))
}

// RecordClusterDriftResult increments the per-row drift classification counter.
// status is the lowercase enum label: in_sync, db_ahead, out_of_band,
// missing_on_resolver, unreachable.
func (r *Recorder) RecordClusterDriftResult(ctx context.Context, status string) {
	if r == nil {
		return
	}
	r.clusterDriftResults.Add(ctx, 1, metric.WithAttributes(attribute.String("status", status)))
}
