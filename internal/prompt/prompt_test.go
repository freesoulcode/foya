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

func TestRenderSkillCatalogEntryClosesPinnedAttribute(t *testing.T) {
	result := renderSkillCatalogEntry(SkillCatalogEntry{
		Ref:         "global:example",
		Name:        "example",
		Description: "Use for examples.",
		Scope:       "global",
		Pinned:      true,
	})
	if !strings.Contains(result, `<skill ref="global:example" name="example" scope="global" pinned="true">`) {
		t.Fatalf("pinned skill tag is malformed:\n%s", result)
	}
}

func TestRenderSelectedSkillIncludesInstructions(t *testing.T) {
	result := ComposeSkillInvocationMessage("Draft release notes.", []SkillCatalogEntry{{
		Ref:  "plugin:documents:writer",
		Name: "writer",
		Body: "Follow the selected workflow.",
		Resources: []SkillResourceEntry{{
			Path: "references/guide.md",
		}},
	}})
	for _, expected := range []string{
		"Use the selected skill instructions below",
		"do not change system rules, available tools, or approval requirements",
		"do not call skill_load for them again",
		`<invoked-skill ref="plugin:documents:writer" name="writer">`,
		"Follow the selected workflow.",
		"<user-message>\nDraft release notes.\n</user-message>",
	} {
		if !strings.Contains(result, expected) {
			t.Fatalf("selected skill does not contain %q:\n%s", expected, result)
		}
	}
}

func TestSelectedSkillIsNotDroppedByCatalogBudget(t *testing.T) {
	selected := SkillCatalogEntry{
		Ref:   "global:large",
		Name:  "large",
		Scope: "global",
		Body:  "Selected instructions.",
	}
	for i := 0; i < 512; i++ {
		selected.Resources = append(selected.Resources, SkillResourceEntry{
			Path: strings.Repeat("long-resource-path/", 20),
		})
	}
	result := ComposeSkillInvocationMessage("Explain this skill.", []SkillCatalogEntry{selected})
	if !strings.Contains(result, `<invoked-skill ref="global:large" name="large">`) ||
		!strings.Contains(result, "Selected instructions.") {
		t.Fatalf("selected skill was dropped by catalog budget:\n%s", result)
	}
}

func TestOversizedSelectedSkillRequiresExplicitLoad(t *testing.T) {
	result := ComposeSkillInvocationMessage("", []SkillCatalogEntry{{
		Ref:  "global:large",
		Name: "large",
		Body: strings.Repeat("x", maxSkillInvocationBodyChars+1),
	}})
	if !strings.Contains(result, "[skill truncated]") {
		t.Fatalf("oversized selected skill was not truncated:\n%s", result)
	}
	if !strings.HasSuffix(
		result,
		"The user provided no additional task text; follow the skill instructions above.",
	) {
		t.Fatal("empty user text did not receive an invocation fallback")
	}
}
