package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAgentLimitsRoundTrip(t *testing.T) {
	dataDir := t.TempDir()
	want := AgentLimits{
		MaxGlobalConcurrency: 8,
		MaxPerRoot:           3,
		MaxTreeTokens:        200_000,
	}
	if err := SaveAgentLimits(dataDir, want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dataDir, "agent-limits.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
	got, ok, err := LoadAgentLimits(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got != want {
		t.Fatalf("loaded = %+v, %v; want %+v", got, ok, want)
	}
}

func TestAgentLimitsRejectsUnknownFields(t *testing.T) {
	dataDir := t.TempDir()
	invalidJSON := `{
  "max_global_concurrency": 5,
  "max_per_root": 2,
  "max_tree_tokens": 50000,
  "max_depth": 5
}`
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "agent-limits.json"), []byte(invalidJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadAgentLimits(dataDir); err == nil {
		t.Fatal("expected unknown field to be rejected")
	}
}

func TestAgentLimitsValidation(t *testing.T) {
	err := ValidateAgentLimits(AgentLimits{
		MaxGlobalConcurrency: 0,
		MaxPerRoot:           1,
	})
	if !errors.Is(err, ErrInvalidAgentLimits) {
		t.Fatalf("error = %v, want ErrInvalidAgentLimits", err)
	}
}
