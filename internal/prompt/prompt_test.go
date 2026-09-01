package prompt

import (
	"strings"
	"testing"
)

func TestEffectiveEnvironmentUsesWSLForRestrictedWindowsMode(t *testing.T) {
	platform, shell := effectiveEnvironment(Input{ApprovalMode: "manual"}, "windows")
	if platform != "linux (WSL2 sandbox on Windows host)" || shell != "/bin/sh" {
		t.Fatalf("platform = %q, shell = %q", platform, shell)
	}
}

func TestEffectiveEnvironmentKeepsNativeWindowsForBypass(t *testing.T) {
	platform, shell := effectiveEnvironment(Input{ApprovalMode: "full_access"}, "windows")
	if platform != "" || shell != "" {
		t.Fatalf("platform = %q, shell = %q", platform, shell)
	}
}

func TestEffectiveEnvironmentKeepsExplicitOverrides(t *testing.T) {
	platform, shell := effectiveEnvironment(Input{
		ApprovalMode: "manual",
		Platform:     "custom-platform",
		Shell:        "custom-shell",
	}, "windows")
	if platform != "custom-platform" || shell != "custom-shell" {
		t.Fatalf("platform = %q, shell = %q", platform, shell)
	}
}

func TestAssembleKeepsRulesAndMemoriesSeparate(t *testing.T) {
	result := Assemble(Input{
		ProjectPath:  t.TempDir(),
		ApprovalMode: "manual",
		HomeDir:      t.TempDir(),
		Rules:        []string{"Always run tests.", "Use Go."},
		Memories:     []string{"The user prefers short answers."},
	})
	for _, expected := range []string{
		"<foya_rules",
		"Always run tests.",
		"<foya_memories",
		"The user prefers short answers.",
	} {
		if !strings.Contains(result, expected) {
			t.Fatalf("prompt does not contain %q", expected)
		}
	}
	if strings.Index(result, "<foya_rules") > strings.Index(result, "<foya_memories") {
		t.Fatal("rules must precede memories")
	}
}
