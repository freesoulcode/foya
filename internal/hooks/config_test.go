package hooks

import (
	"errors"
	"os"
	"testing"
)

func TestConfigRoundTrip(t *testing.T) {
	homeDir := t.TempDir()
	enabled := true
	want := []Config{{
		ID:      "format-bash",
		Name:    "Format bash command",
		Event:   EventPreToolUse,
		Matcher: "^bash$",
		Command: "foya-hook-format",
		Timeout: 10,
		Async:   true,
		Enabled: &enabled,
	}}

	if err := SaveGlobal(homeDir, want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(GlobalConfigPath(homeDir))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}

	got, ok, err := LoadGlobal(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || len(got) != 1 {
		t.Fatalf("loaded = %+v, %v; want one hook", got, ok)
	}
	if got[0].ID != want[0].ID || got[0].Matcher != want[0].Matcher ||
		got[0].Timeout != want[0].Timeout || !got[0].Async {
		t.Fatalf("loaded = %+v, want %+v", got[0], want[0])
	}
}

func TestGlobalAndProjectScopes(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	global := []Config{{ID: "global", Event: EventSessionStart, Command: "echo global"}}
	project := []Config{{ID: "project", Event: EventPreToolUse, Command: "echo project"}}

	if err := SaveGlobal(homeDir, global); err != nil {
		t.Fatal(err)
	}
	if err := SaveProject(projectDir, project); err != nil {
		t.Fatal(err)
	}

	got, err := LoadForProject(homeDir, projectDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "global" || got[1].ID != "project" {
		t.Fatalf("loaded hooks = %+v, want global then project", got)
	}
	if _, err := os.Stat(GlobalConfigPath(homeDir)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ProjectConfigPath(projectDir)); err != nil {
		t.Fatal(err)
	}
}

func TestLoadForProjectAllowsMissingScopes(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	if err := SaveGlobal(homeDir, []Config{{Event: EventNotification, Command: "echo notify"}}); err != nil {
		t.Fatal(err)
	}

	got, err := LoadForProject(homeDir, projectDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Event != EventNotification {
		t.Fatalf("loaded hooks = %+v, want global hook", got)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	homeDir := t.TempDir()
	if err := os.MkdirAll(homeDir+"/.foya", 0o700); err != nil {
		t.Fatal(err)
	}
	data := []byte(`[{"event":"PreToolUse","command":"echo ok","unexpected":true}]`)
	if err := os.WriteFile(GlobalConfigPath(homeDir), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadGlobal(homeDir); err == nil {
		t.Fatal("expected unknown field to be rejected")
	}
}

func TestValidateConfig(t *testing.T) {
	for _, event := range []Event{
		EventSessionStart,
		EventSessionEnd,
		EventUserPromptSubmit,
		EventPreToolUse,
		EventPostToolUse,
		EventPermissionRequest,
		EventSubagentStart,
		EventSubagentStop,
		EventPreCompact,
		EventPostCompact,
		EventStop,
		EventTurnComplete,
		EventNotification,
	} {
		t.Run(string(event), func(t *testing.T) {
			if err := Validate([]Config{{Event: event, Command: "echo ok"}}); err != nil {
				t.Fatalf("event should be valid: %v", err)
			}
		})
	}

	tests := []struct {
		name  string
		item  Config
		error error
	}{
		{name: "missing command", item: Config{Event: EventPreToolUse}, error: ErrInvalidHook},
		{name: "unsupported event", item: Config{Event: Event("UnknownEvent"), Command: "echo ok"}, error: ErrInvalidEvent},
		{name: "invalid matcher", item: Config{Event: EventPreToolUse, Command: "echo ok", Matcher: "["}, error: ErrInvalidHooksConfig},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := Validate([]Config{tt.item}); !errors.Is(err, tt.error) {
				t.Fatalf("error = %v, want %v", err, tt.error)
			}
		})
	}
}

func TestEventPolicy(t *testing.T) {
	for _, event := range []Event{
		EventSessionEnd,
		EventPostCompact,
		EventTurnComplete,
		EventNotification,
	} {
		if event.SupportsControlEffects() || !event.IsAsync() {
			t.Fatalf("%s must be asynchronous and observational", event)
		}
	}
	if EventPreToolUse.IsAsync() || !EventPreToolUse.SupportsControlEffects() {
		t.Fatal("PreToolUse must be synchronous and controlling")
	}
}

func TestConfigMatchingAndDefaults(t *testing.T) {
	disabled := false
	all := Config{Event: EventPreToolUse, Command: "echo ok"}
	if !all.MatchesTool("bash") || all.TimeoutDuration().Seconds() != 30 {
		t.Fatal("unexpected default behavior")
	}
	bash := Config{Event: EventPreToolUse, Command: "echo ok", Matcher: "^bash$"}
	if !bash.MatchesTool("bash") || bash.MatchesTool("write") {
		t.Fatal("matcher did not select expected tool")
	}
	bash.Enabled = &disabled
	if bash.MatchesTool("bash") {
		t.Fatal("disabled hook should not match")
	}
}
