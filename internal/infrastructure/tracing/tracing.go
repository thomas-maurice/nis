// Package tracing initialises an OpenTelemetry tracer provider that exports
// spans over OTLP/gRPC. The package is a no-op when tracing is disabled —
// `otel.GetTracerProvider()` continues to return the no-op provider so that
// `otel.Tracer(...)` calls anywhere in the program are safe and free.
//
// Lifecycle:
//
//   shutdown, err := tracing.Init(ctx, tracing.Config{
//       Enabled:  true,
//       Endpoint: "localhost:4317",
//       Service:  "nis",
//       Version:  version,
//       Sampler:  1.0,
//   })
//   defer shutdown(context.Background())
//
// See README.md "Observability" for the recommended Jaeger / Tempo setup.
package tracing

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
)

// Config controls tracer initialisation.
type Config struct {
	// Enabled flips the whole subsystem. When false, Init returns a no-op shutdown.
	Enabled bool

	// Endpoint is the OTLP/gRPC collector address, e.g. "localhost:4317".
	// Ignored when Enabled is false.
	Endpoint string

	// Insecure disables TLS for the OTLP connection. Default for local dev with
	// Jaeger/Tempo collectors that don't terminate TLS.
	Insecure bool

	// Service is the resource service.name (defaults to "nis").
	Service string

	// Version is the resource service.version (typically the git tag).
	Version string

	// Sampler is the parent-based ratio sampler ratio in [0,1]. 1.0 = sample
	// every trace. Use lower values in production to bound exporter load.
	Sampler float64
}

// ShutdownFunc flushes any pending spans and tears the tracer provider down.
type ShutdownFunc func(ctx context.Context) error

// Init configures the global OTel tracer provider and propagator. Returns a
// no-op shutdown when Config.Enabled is false.
func Init(ctx context.Context, cfg Config) (ShutdownFunc, error) {
	if !cfg.Enabled {
		return func(context.Context) error { return nil }, nil
	}
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("tracing: endpoint is required when enabled")
	}
	if cfg.Service == "" {
		cfg.Service = "nis"
	}
	if cfg.Sampler <= 0 {
		cfg.Sampler = 1.0
	}

	opts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(cfg.Endpoint)}
	if cfg.Insecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}

	exporter, err := otlptracegrpc.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("create OTLP exporter: %w", err)
	}

	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(cfg.Service),
		semconv.ServiceVersion(cfg.Version),
	))
	if err != nil {
		return nil, fmt.Errorf("build resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.Sampler))),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return tp.Shutdown, nil
}
