package hooks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRuntimeMergesGlobalAndProjectPreToolHooks(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	if err := SaveGlobal(homeDir, []Config{{
		ID:      "global",
		Event:   EventPreToolUse,
		Matcher: "^bash$",
		Command: `printf '{"context":"global","updated_input":{"timeout":10}}'`,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := SaveProject(projectDir, []Config{{
		ID:      "project",
		Event:   EventPreToolUse,
		Matcher: "^bash$",
		Command: `printf '{"context":["project"],"updated_input":{"command":"pwd"}}'`,
	}}); err != nil {
		t.Fatal(err)
	}

	outcome := NewRuntime(homeDir).Run(context.Background(), Request{
		Event:       EventPreToolUse,
		ProjectPath: projectDir,
		ToolName:    "bash",
		ToolInput:   []byte(`{"command":"ls"}`),
	})
	if outcome.Decision != DecisionNone {
		t.Fatalf("decision = %q, want none", outcome.Decision)
	}
	if len(outcome.Results) != 2 || outcome.Results[0].ID != "global" || outcome.Results[1].ID != "project" {
		t.Fatalf("results = %+v, want global then project", outcome.Results)
	}
	if string(outcome.UpdatedInput) != `{"command":"pwd","timeout":10}` {
		t.Fatalf("updated input = %s", outcome.UpdatedInput)
	}
	if len(outcome.Context) != 2 || outcome.Context[0] != "global" || outcome.Context[1] != "project" {
		t.Fatalf("context = %#v", outcome.Context)
	}
}

func TestRuntimeDenyAndHaltExitCodes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses POSIX shell exit syntax")
	}
	homeDir := t.TempDir()
	if err := SaveGlobal(homeDir, []Config{{
		Event: EventPreToolUse, Command: `echo forbidden >&2; exit 2`,
	}}); err != nil {
		t.Fatal(err)
	}
	outcome := NewRuntime(homeDir).Run(context.Background(), Request{
		Event: EventPreToolUse, ToolName: "bash",
	})
	if outcome.Decision != DecisionDeny || outcome.Halt || outcome.Reason != "forbidden" {
		t.Fatalf("deny outcome = %+v", outcome)
	}

	if err := SaveGlobal(homeDir, []Config{{
		Event: EventStop, Command: `echo stop >&2; exit 49`,
	}}); err != nil {
		t.Fatal(err)
	}
	outcome = NewRuntime(homeDir).Run(context.Background(), Request{Event: EventStop})
	if outcome.Decision != DecisionDeny || !outcome.Halt || outcome.Reason != "stop" {
		t.Fatalf("halt outcome = %+v", outcome)
	}
}

func TestRuntimeHookFailuresFailOpen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses POSIX shell syntax")
	}
	homeDir := t.TempDir()
	if err := SaveGlobal(homeDir, []Config{{
		Event: EventUserPromptSubmit, Command: "exit 1",
	}}); err != nil {
		t.Fatal(err)
	}
	outcome := NewRuntime(homeDir).Run(context.Background(), Request{
		Event: EventUserPromptSubmit,
	})
	if outcome.Decision != DecisionNone || len(outcome.Results) != 1 || outcome.Results[0].Error == "" {
		t.Fatalf("outcome = %+v, want fail-open result", outcome)
	}
}

func TestRuntimeNotificationIsAsync(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses POSIX shell syntax")
	}
	homeDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "notification")
	if err := SaveGlobal(homeDir, []Config{{
		Event: EventNotification, Command: "sleep 0.1; touch " + marker,
	}}); err != nil {
		t.Fatal(err)
	}

	done := make(chan Outcome, 1)
	started := time.Now()
	NewRuntime(homeDir).Notify(context.Background(), Request{
		Event: EventNotification, Notification: "turn_complete",
	}, func(outcome Outcome) {
		done <- outcome
	})
	if elapsed := time.Since(started); elapsed > 50*time.Millisecond {
		t.Fatalf("Notify blocked for %s", elapsed)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("notification hook did not complete")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("notification marker: %v", err)
	}
}

func TestRuntimeObservationalEventIsAsync(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses POSIX shell syntax")
	}
	homeDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "turn-complete")
	if err := SaveGlobal(homeDir, []Config{{
		Event: EventTurnComplete, Command: "sleep 0.1; touch " + marker,
	}}); err != nil {
		t.Fatal(err)
	}

	done := make(chan Outcome, 1)
	started := time.Now()
	NewRuntime(homeDir).Observe(context.Background(), Request{
		Event: EventTurnComplete,
	}, func(outcome Outcome) {
		done <- outcome
	})
	if elapsed := time.Since(started); elapsed > 50*time.Millisecond {
		t.Fatalf("Observe blocked for %s", elapsed)
	}
	select {
	case outcome := <-done:
		if len(outcome.Results) != 1 || outcome.Results[0].Error != "" {
			t.Fatalf("outcome = %+v", outcome)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("observational hook did not complete")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("observation marker: %v", err)
	}
}

func TestRuntimeAsyncHandlerReportsCompletion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses POSIX shell syntax")
	}
	homeDir := t.TempDir()
	if err := SaveGlobal(homeDir, []Config{{
		Event: EventPreToolUse, Command: "sleep 0.1", Async: true,
	}}); err != nil {
		t.Fatal(err)
	}

	done := make(chan Outcome, 1)
	started := time.Now()
	outcome := NewRuntime(homeDir).RunWithCompletion(
		context.Background(),
		Request{Event: EventPreToolUse, ToolName: "bash"},
		func(outcome Outcome) { done <- outcome },
	)
	if len(outcome.Results) != 0 {
		t.Fatalf("synchronous results = %+v, want none", outcome.Results)
	}
	if elapsed := time.Since(started); elapsed > 50*time.Millisecond {
		t.Fatalf("async handler blocked for %s", elapsed)
	}
	select {
	case asyncOutcome := <-done:
		if len(asyncOutcome.Results) != 1 || asyncOutcome.Results[0].Error != "" {
			t.Fatalf("async outcome = %+v", asyncOutcome)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("async hook did not report completion")
	}
}

func TestRuntimeMatchesLifecycleField(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses POSIX shell syntax")
	}
	homeDir := t.TempDir()
	if err := SaveGlobal(homeDir, []Config{{
		Event: EventPreCompact, Matcher: "^auto$", Command: "true",
	}}); err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(homeDir)
	if outcome := runtime.Run(context.Background(), Request{
		Event: EventPreCompact, CompactionTrigger: "manual",
	}); len(outcome.Results) != 0 {
		t.Fatalf("manual outcome = %+v, want no match", outcome)
	}
	if outcome := runtime.Run(context.Background(), Request{
		Event: EventPreCompact, CompactionTrigger: "auto",
	}); len(outcome.Results) != 1 {
		t.Fatalf("auto outcome = %+v, want one match", outcome)
	}
}

func TestRuntimeAddsStableEnvelopeFields(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses POSIX shell syntax")
	}
	homeDir := t.TempDir()
	payloadPath := filepath.Join(t.TempDir(), "request.json")
	if err := SaveGlobal(homeDir, []Config{{
		Event: EventSessionStart, Command: "cat > " + payloadPath,
	}}); err != nil {
		t.Fatal(err)
	}
	outcome := NewRuntime(homeDir).Run(context.Background(), Request{
		Event: EventSessionStart,
	})
	if len(outcome.Results) != 1 || outcome.Results[0].Error != "" {
		t.Fatalf("outcome = %+v", outcome)
	}
	data, err := os.ReadFile(payloadPath)
	if err != nil {
		t.Fatal(err)
	}
	var request Request
	if err := json.Unmarshal(data, &request); err != nil {
		t.Fatal(err)
	}
	if request.Version != 1 || request.EventID == "" || request.OccurredAt.IsZero() {
		t.Fatalf("request envelope = %+v", request)
	}
}

func TestHookEnvironmentFiltersTelemetryCredentials(t *testing.T) {
	t.Setenv("LANGFUSE_AUTH", "secret")
	t.Setenv("LANGFUSE_SECRET_KEY", "secret")
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "Authorization=secret")
	t.Setenv("SAFE_VALUE", "visible")

	env := hookEnvironment(Request{})
	joined := strings.Join(env, "\n")
	for _, secret := range []string{
		"LANGFUSE_AUTH=",
		"LANGFUSE_SECRET_KEY=",
		"OTEL_EXPORTER_OTLP_HEADERS=",
	} {
		if strings.Contains(joined, secret) {
			t.Fatalf("sensitive environment variable leaked: %s", secret)
		}
	}
	if !strings.Contains(joined, "SAFE_VALUE=visible") {
		t.Fatal("non-sensitive environment variable was removed")
	}
}
