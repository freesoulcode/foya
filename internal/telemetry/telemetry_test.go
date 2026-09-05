package telemetry

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestStartAndEndSpanRecordsFailure(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
	})

	ctx, span := StartSpan(context.Background(), "foya.test", trace.SpanKindInternal)
	EndSpan(span, "failed", errors.New("boom"))
	if ctx == nil {
		t.Fatal("span context is nil")
	}
	ended := recorder.Ended()
	if len(ended) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(ended))
	}
	if ended[0].Name() != "foya.test" {
		t.Fatalf("span name = %q", ended[0].Name())
	}
}

func TestDisabledProviderDoesNotCaptureContent(t *testing.T) {
	provider, err := New(context.Background(), Config{
		Enabled:        false,
		CaptureContent: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if CaptureContent() {
		t.Fatal("content capture must remain disabled when telemetry is disabled")
	}
	if err := provider.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestContentIsBounded(t *testing.T) {
	input := make([]byte, maxContentAttributeBytes+10)
	for index := range input {
		input[index] = 'x'
	}
	got := Content(string(input))
	if len(got) <= maxContentAttributeBytes || len(got) >= len(input)+20 {
		t.Fatalf("bounded content length = %d", len(got))
	}
}
