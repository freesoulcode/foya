// Package telemetry owns Foya's OpenTelemetry providers and stable
// instrumentation names. Exporter transport is configured through standard
// OTEL_EXPORTER_OTLP_* environment variables.
package telemetry

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.38.0"
	"go.opentelemetry.io/otel/trace"
)

const instrumentationName = "github.com/freesoulcode/foya"
const maxContentAttributeBytes = 64 << 10

type Config struct {
	Enabled        bool
	TracesEnabled  bool
	MetricsEnabled bool
	CaptureContent bool
	ServiceName    string
	Environment    string
}

type Provider struct {
	traces  *sdktrace.TracerProvider
	metrics *sdkmetric.MeterProvider
}

var captureContent atomic.Bool

var (
	tracer = otel.Tracer(instrumentationName)
	meter  = otel.Meter(instrumentationName)

	turnCount, _          = meter.Int64Counter("foya.turn.count")
	turnDuration, _       = meter.Float64Histogram("foya.turn.duration", metric.WithUnit("ms"))
	llmCount, _           = meter.Int64Counter("foya.llm.count")
	llmDuration, _        = meter.Float64Histogram("foya.llm.duration", metric.WithUnit("ms"))
	llmTTFT, _            = meter.Float64Histogram("foya.llm.ttft", metric.WithUnit("ms"))
	llmTokens, _          = meter.Int64Histogram("foya.llm.token.usage", metric.WithUnit("{token}"))
	llmErrors, _          = meter.Int64Counter("foya.llm.error")
	toolDuration, _       = meter.Float64Histogram("foya.tool.duration", metric.WithUnit("ms"))
	toolCalls, _          = meter.Int64Counter("foya.tool.count")
	approvalDuration, _   = meter.Float64Histogram("foya.approval.wait_duration", metric.WithUnit("ms"))
	hookDuration, _       = meter.Float64Histogram("foya.hook.duration", metric.WithUnit("ms"))
	hookErrors, _         = meter.Int64Counter("foya.hook.error")
	compactionCount, _    = meter.Int64Counter("foya.compaction.count")
	compactionDuration, _ = meter.Float64Histogram("foya.compaction.duration", metric.WithUnit("ms"))
	queueWaitDuration, _  = meter.Float64Histogram("foya.queue.wait_duration", metric.WithUnit("ms"))
)

func New(ctx context.Context, cfg Config) (*Provider, error) {
	provider := &Provider{}
	captureContent.Store(cfg.Enabled && cfg.CaptureContent)
	if !cfg.Enabled {
		return provider, nil
	}

	serviceName := cfg.ServiceName
	if serviceName == "" {
		serviceName = "foya"
	}
	environment := cfg.Environment
	if environment == "" {
		environment = "development"
	}
	res, err := resource.New(
		ctx,
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			attribute.String("deployment.environment.name", environment),
		),
	)
	if err != nil {
		return nil, err
	}

	if cfg.TracesEnabled {
		exporter, exportErr := otlptracehttp.New(ctx)
		if exportErr != nil {
			return nil, exportErr
		}
		provider.traces = sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(exporter),
			sdktrace.WithResource(res),
		)
		otel.SetTracerProvider(provider.traces)
	}
	if cfg.MetricsEnabled {
		exporter, exportErr := otlpmetrichttp.New(ctx)
		if exportErr != nil {
			_ = provider.Shutdown(context.Background())
			return nil, exportErr
		}
		provider.metrics = sdkmetric.NewMeterProvider(
			sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter)),
			sdkmetric.WithResource(res),
		)
		otel.SetMeterProvider(provider.metrics)
	}
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	return provider, nil
}

func (p *Provider) Shutdown(ctx context.Context) error {
	if p == nil {
		return nil
	}
	var errs []error
	if p.metrics != nil {
		errs = append(errs, p.metrics.Shutdown(ctx))
	}
	if p.traces != nil {
		errs = append(errs, p.traces.Shutdown(ctx))
	}
	return errors.Join(errs...)
}

func CaptureContent() bool {
	return captureContent.Load()
}

func Content(value string) string {
	if len(value) <= maxContentAttributeBytes {
		return value
	}
	return value[:maxContentAttributeBytes] + "\n[truncated]"
}

func StartSpan(
	ctx context.Context,
	name string,
	kind trace.SpanKind,
	attrs ...attribute.KeyValue,
) (context.Context, trace.Span) {
	return tracer.Start(
		ctx,
		name,
		trace.WithSpanKind(kind),
		trace.WithAttributes(attrs...),
	)
}

func EndSpan(span trace.Span, status string, err error, attrs ...attribute.KeyValue) {
	if len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	} else if status == "failed" {
		span.SetStatus(codes.Error, status)
	} else {
		span.SetStatus(codes.Ok, status)
	}
	span.End()
}

func RecordTurn(ctx context.Context, elapsed time.Duration, attrs ...attribute.KeyValue) {
	options := metric.WithAttributes(attrs...)
	turnCount.Add(ctx, 1, options)
	turnDuration.Record(ctx, float64(elapsed.Microseconds())/1000, options)
}

func RecordLLM(
	ctx context.Context,
	elapsed, ttft time.Duration,
	input, output, cached int64,
	failed bool,
	attrs ...attribute.KeyValue,
) {
	options := metric.WithAttributes(attrs...)
	llmCount.Add(ctx, 1, options)
	llmDuration.Record(ctx, float64(elapsed.Microseconds())/1000, options)
	if ttft > 0 {
		llmTTFT.Record(ctx, float64(ttft.Microseconds())/1000, options)
	}
	for tokenType, count := range map[string]int64{
		"input": input, "output": output, "cached_input": cached,
	} {
		if count > 0 {
			tokenAttrs := append([]attribute.KeyValue(nil), attrs...)
			tokenAttrs = append(tokenAttrs, attribute.String("token.type", tokenType))
			llmTokens.Record(ctx, count, metric.WithAttributes(
				tokenAttrs...,
			))
		}
	}
	if failed {
		llmErrors.Add(ctx, 1, options)
	}
}

func RecordTool(ctx context.Context, elapsed time.Duration, attrs ...attribute.KeyValue) {
	options := metric.WithAttributes(attrs...)
	toolDuration.Record(ctx, float64(elapsed.Microseconds())/1000, options)
	toolCalls.Add(ctx, 1, options)
}

func RecordApproval(ctx context.Context, elapsed time.Duration, attrs ...attribute.KeyValue) {
	approvalDuration.Record(
		ctx,
		float64(elapsed.Microseconds())/1000,
		metric.WithAttributes(attrs...),
	)
}

func RecordHook(ctx context.Context, elapsed time.Duration, failed bool, attrs ...attribute.KeyValue) {
	options := metric.WithAttributes(attrs...)
	hookDuration.Record(ctx, float64(elapsed.Microseconds())/1000, options)
	if failed {
		hookErrors.Add(ctx, 1, options)
	}
}

func RecordCompaction(ctx context.Context, elapsed time.Duration, attrs ...attribute.KeyValue) {
	options := metric.WithAttributes(attrs...)
	compactionCount.Add(ctx, 1, options)
	compactionDuration.Record(ctx, float64(elapsed.Microseconds())/1000, options)
}

func RecordQueueWait(ctx context.Context, elapsed time.Duration, attrs ...attribute.KeyValue) {
	queueWaitDuration.Record(
		ctx,
		float64(elapsed.Microseconds())/1000,
		metric.WithAttributes(attrs...),
	)
}
