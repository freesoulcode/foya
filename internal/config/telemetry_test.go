package config

import "testing"

func TestDefaultTelemetryFromEnvironment(t *testing.T) {
	t.Setenv("FOYA_OTEL_ENABLED", "true")
	t.Setenv("FOYA_OTEL_TRACES_ENABLED", "false")
	t.Setenv("FOYA_OTEL_METRICS_ENABLED", "true")
	t.Setenv("FOYA_OTEL_CAPTURE_CONTENT", "true")
	t.Setenv("OTEL_SERVICE_NAME", "foya-test")
	t.Setenv("FOYA_OTEL_ENVIRONMENT", "test")

	got := Default().Telemetry
	if !got.Enabled || got.TracesEnabled || !got.MetricsEnabled || !got.CaptureContent {
		t.Fatalf("telemetry flags = %+v", got)
	}
	if got.ServiceName != "foya-test" || got.Environment != "test" {
		t.Fatalf("telemetry identity = %+v", got)
	}
}

func TestDefaultTelemetryIsDisabled(t *testing.T) {
	t.Setenv("FOYA_OTEL_ENABLED", "")
	t.Setenv("FOYA_OTEL_TRACES_ENABLED", "")
	t.Setenv("FOYA_OTEL_METRICS_ENABLED", "")
	t.Setenv("FOYA_OTEL_CAPTURE_CONTENT", "")

	got := Default().Telemetry
	if got.Enabled || !got.TracesEnabled || got.MetricsEnabled || got.CaptureContent {
		t.Fatalf("telemetry defaults = %+v", got)
	}
}
