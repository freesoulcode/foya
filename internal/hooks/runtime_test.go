package hooks

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
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
