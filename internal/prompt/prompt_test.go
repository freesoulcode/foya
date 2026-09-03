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

func TestAssembleIncludesBoundedSkillCatalogMetadata(t *testing.T) {
	result := Assemble(Input{
		ProjectPath:  t.TempDir(),
		ApprovalMode: "manual",
		HomeDir:      t.TempDir(),
		Skills: []SkillCatalogEntry{
			{
				Ref:          "builtin:example-skill",
				Name:         "example-skill",
				Description:  "Use for example tasks.",
				Scope:        "builtin",
				AllowedTools: []string{"example_tool"},
			},
		},
	})
	for _, expected := range []string{
		"<available_skills",
		"skill_load",
		"not a procedure",
		"until the matching skill has been loaded",
		"builtin:example-skill",
		"Use for example tasks.",
		"example_tool",
	} {
		if !strings.Contains(result, expected) {
			t.Fatalf("prompt does not contain %q", expected)
		}
	}
	if strings.Contains(result, "Take a snapshot before interacting.") {
		t.Fatal("skill catalog should not include full skill bodies")
	}
}

func TestSkillCatalogFragmentIsBounded(t *testing.T) {
	skills := make([]SkillCatalogEntry, 0, 128)
	for i := 0; i < 128; i++ {
		skills = append(skills, SkillCatalogEntry{
			Ref:         "global:skill",
			Name:        "skill",
			Scope:       "global",
			Description: strings.Repeat("long description ", 256),
		})
	}
	result := skillsCatalogFragment(skills)
	if len(result) > maxSkillsCatalogChars {
		t.Fatalf("catalog length = %d, want <= %d", len(result), maxSkillsCatalogChars)
	}
	if !strings.Contains(result, "omitted due to prompt budget") {
		t.Fatal("catalog should report omitted skills")
	}
}
