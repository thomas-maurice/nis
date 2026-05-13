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
	return &r, nil
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
