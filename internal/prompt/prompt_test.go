package prompt

import "testing"

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
