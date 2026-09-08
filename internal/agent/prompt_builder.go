package agent

import (
	"os"
	"runtime"
	"strings"
	"time"
)

// promptInput contains everything needed to assemble one system prompt.
//
// Environment-related zero values receive runtime defaults so callers only
// need to provide relevant chat-level settings.
type promptInput struct {
	ProjectPath  string   // Current project directory.
	ApprovalMode string   // manual, auto, or full_access.
	Rules        []string // Global and project rules managed by Foya.
	RuleIndex    []string // Rules available for on-demand model loading.
	Memories     []string // Global and project memories managed by Foya.
	Skills       []skillCatalogEntry
	Platform     string    // Inferred when empty.
	Shell        string    // Inferred when empty.
	Now          time.Time // Uses time.Now when empty.
	HomeDir      string    // Uses os.UserHomeDir when empty.
}

// assemblePrompt builds the system prompt from stable and dynamic fragments.
//
// The static prefix remains byte-stable for provider caching. User-controlled
// context is bounded and marked with explicit authority boundaries.
func assemblePrompt(in promptInput) string {
	home := in.HomeDir
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	platform, shell := effectiveEnvironment(in, runtime.GOOS)

	fragments := []string{
		staticPrefix,
		loadProjectInstructions(home, in.ProjectPath),
		skillsCatalogFragment(in.Skills),
		managedRulesFragment(in.Rules),
		managedRuleIndexFragment(in.RuleIndex),
		permissionFragment(in.ApprovalMode),
		managedMemoriesFragment(in.Memories),
		envFragment(promptEnvironmentInput{
			Cwd:      in.ProjectPath,
			Platform: platform,
			Shell:    shell,
			Now:      now,
		}),
	}

	return joinFragments(fragments)
}

func effectiveEnvironment(in promptInput, goos string) (platform, shell string) {
	platform = in.Platform
	shell = in.Shell
	if goos == "windows" && in.ApprovalMode != "full_access" {
		if platform == "" {
			platform = "linux (WSL2 sandbox on Windows host)"
		}
		if shell == "" {
			shell = "/bin/sh"
		}
	}
	return platform, shell
}

// joinFragments joins non-empty sections with a blank line.
func joinFragments(parts []string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "\n\n")
}
