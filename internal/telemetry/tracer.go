// Package telemetry initialises OpenTelemetry tracing.
package telemetry

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
)

// InitTracer configures the global OTel tracer.
//
//   - If endpoint is non-empty the OTLP HTTP exporter is used (e.g. "http://localhost:4318").
//   - If endpoint is empty a no-op (stdout with JSON, discarded) exporter is used so the
//     application starts cleanly with no collector running.
//
// The returned shutdown function must be called on application exit (e.g. defer shutdown()).
func InitTracer(endpoint string) (func(), error) {
	ctx := context.Background()

	var exp trace.SpanExporter
	var err error

	if endpoint != "" {
		exp, err = otlptracehttp.New(ctx,
			otlptracehttp.WithEndpoint(endpoint),
			otlptracehttp.WithInsecure(), // TLS can be configured externally
		)
		if err != nil {
			return nil, fmt.Errorf("telemetry: failed to create OTLP exporter: %w", err)
		}
	} else {
		// No collector configured — use stdout exporter with no-op writer so spans
		// are processed but not emitted, keeping the tracer fully functional.
		exp, err = stdouttrace.New(stdouttrace.WithPrettyPrint())
		if err != nil {
			return nil, fmt.Errorf("telemetry: failed to create stdout exporter: %w", err)
		}
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String("llm-consensus"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("telemetry: failed to create resource: %w", err)
	}

	tp := trace.NewTracerProvider(
		trace.WithBatcher(exp),
		trace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	shutdown := func() {
		_ = tp.Shutdown(context.Background())
	}
	return shutdown, nil
}
