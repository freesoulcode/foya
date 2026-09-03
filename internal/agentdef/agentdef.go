// Package agentdef discovers reusable agent definitions from user and project scopes.
package agentdef

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	maxDefinitionBytes = 256 * 1024
	maxAgentsPerRoot   = 256
)

type Scope string

const (
	ScopeBuiltin Scope = "builtin"
	ScopeUser    Scope = "user"
	ScopeProject Scope = "project"
)

var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// Definition is an immutable agent profile resolved before a child session starts.
type Definition struct {
	Ref         string   `json:"ref"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Scope       Scope    `json:"scope"`
	Path        string   `json:"path,omitempty"`
	Model       string   `json:"model,omitempty"`
	Tools       []string `json:"tools,omitempty"`
	MaxTurns    int      `json:"max_turns,omitempty"`
	Timeout     string   `json:"timeout,omitempty"`
	Body        string   `json:"-"`
	Digest      string   `json:"digest"`
}

type frontmatter struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Model       string   `yaml:"model"`
	Tools       []string `yaml:"tools"`
	MaxTurns    int      `yaml:"max_turns"`
	Timeout     string   `yaml:"timeout"`
}

// Manager owns definition discovery. Definitions are read on every request so
// editing a Markdown file does not require restarting the kernel.
type Manager struct {
	homeDir  string
	builtins []Definition
}

func NewManager(homeDir string, builtins []Definition) *Manager {
	cloned := append([]Definition(nil), builtins...)
	for i := range cloned {
		cloned[i].Scope = ScopeBuiltin
		cloned[i].Ref = definitionRef(ScopeBuiltin, "", cloned[i].Name)
		cloned[i].Digest = definitionDigest(cloned[i])
	}
	return &Manager{homeDir: homeDir, builtins: cloned}
}

// BuiltinDefinitions returns the standard roles available to every parent agent.
func BuiltinDefinitions() []Definition {
	return []Definition{
		ExplorerDefinition(),
		ResearcherDefinition(),
		WorkerDefinition(),
	}
}

func ExplorerDefinition() Definition {
	return builtinDefinition(
		"explorer",
		"Inspect the codebase and return evidence-backed findings without changing files.",
		[]string{"read", "skill_search", "skill_load", "skill_read_resource"},
		"Investigate the delegated codebase question. Read relevant files and return concise findings with concrete paths and symbols. Do not modify files, run commands, ask the user questions, or delegate to another agent.",
	)
}

func ResearcherDefinition() Definition {
	return builtinDefinition(
		"researcher",
		"Research external sources and return cited findings.",
		[]string{"web_search", "web_fetch", "skill_search", "skill_load", "skill_read_resource"},
		"Research the delegated topic using authoritative external sources. Return concise findings with source URLs, uncertainties, and clear separation between sourced facts and inference. Do not modify files, ask the user questions, or delegate to another agent.",
	)
}

func WorkerDefinition() Definition {
	return builtinDefinition(
		"worker",
		"Complete an implementation task, including code changes and verification.",
		[]string{
			"read", "bash", "write", "edit",
			"skill_search", "skill_load", "skill_read_resource",
		},
		"Complete the delegated implementation task end to end. Inspect relevant code before editing, keep changes scoped, and run focused verification. Other agents may share the workspace, so do not revert changes you did not make. Do not ask the user questions or delegate to another agent.",
	)
}

func builtinDefinition(name, description string, tools []string, body string) Definition {
	def := Definition{
		Name:        name,
		Description: description,
		Scope:       ScopeBuiltin,
		Tools:       tools,
		Body:        body,
	}
	def.Ref = definitionRef(def.Scope, "", def.Name)
	def.Digest = definitionDigest(def)
	return def
}

// List returns effective definitions. Project definitions take precedence over
// user and builtin definitions when callers use an unqualified name.
func (m *Manager) List(ctx context.Context, projectID, projectPath string) ([]Definition, error) {
	all, err := m.ListAll(ctx, projectID, projectPath)
	if err != nil {
		return nil, err
	}
	effective := make(map[string]Definition)
	for _, item := range all {
		key := strings.ToLower(item.Name)
		current, exists := effective[key]
		if !exists || scopeRank(item.Scope) > scopeRank(current.Scope) {
			effective[key] = item
		}
	}
	out := make([]Definition, 0, len(effective))
	for _, item := range effective {
		out = append(out, item)
	}
	sortDefinitions(out)
	return out, nil
}

// ListAll returns all scopes so management clients can display provenance.
func (m *Manager) ListAll(_ context.Context, projectID, projectPath string) ([]Definition, error) {
	out := append([]Definition(nil), m.builtins...)
	if m.homeDir != "" {
		items, err := scanRoot(filepath.Join(m.homeDir, ".agents", "agents"), ScopeUser, "")
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
	}
	if projectPath != "" {
		if strings.TrimSpace(projectID) == "" {
			return nil, errors.New("project ID is required for project agents")
		}
		items, err := scanRoot(filepath.Join(projectPath, ".agents", "agents"), ScopeProject, projectID)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
	}
	sortDefinitions(out)
	return out, nil
}

func (m *Manager) Get(
	ctx context.Context,
	projectID, projectPath, refOrName string,
) (Definition, error) {
	items, err := m.ListAll(ctx, projectID, projectPath)
	if err != nil {
		return Definition{}, err
	}
	var named *Definition
	for i := range items {
		item := items[i]
		if item.Ref == refOrName {
			return item, nil
		}
		if strings.EqualFold(item.Name, refOrName) &&
			(named == nil || scopeRank(item.Scope) > scopeRank(named.Scope)) {
			copy := item
			named = &copy
		}
	}
	if named != nil {
		return *named, nil
	}
	return Definition{}, fs.ErrNotExist
}

func scanRoot(root string, scope Scope, projectID string) ([]Definition, error) {
	info, err := os.Stat(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	out := make([]Definition, 0, len(entries))
	names := make(map[string]string)
	for _, entry := range entries {
		if len(out) >= maxAgentsPerRoot {
			break
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 ||
			!strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			continue
		}
		item, err := parseFile(filepath.Join(root, entry.Name()), scope, projectID)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", entry.Name(), err)
		}
		key := strings.ToLower(item.Name)
		if previous, exists := names[key]; exists {
			return nil, fmt.Errorf(
				"duplicate agent name %q in %s and %s",
				item.Name, previous, entry.Name(),
			)
		}
		names[key] = entry.Name()
		out = append(out, item)
	}
	return out, nil
}

func parseFile(path string, scope Scope, projectID string) (Definition, error) {
	file, err := os.Open(path)
	if err != nil {
		return Definition{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxDefinitionBytes+1))
	if err != nil {
		return Definition{}, err
	}
	if len(data) > maxDefinitionBytes {
		return Definition{}, fmt.Errorf("agent definition exceeds %d bytes", maxDefinitionBytes)
	}
	meta, body, err := parseDocument(string(data))
	if err != nil {
		return Definition{}, err
	}
	if !validName.MatchString(meta.Name) {
		return Definition{}, fmt.Errorf("invalid agent name %q", meta.Name)
	}
	if strings.TrimSpace(meta.Description) == "" {
		return Definition{}, errors.New("agent description is required")
	}
	if strings.TrimSpace(body) == "" {
		return Definition{}, errors.New("agent instructions are required")
	}
	if meta.MaxTurns < 0 {
		return Definition{}, errors.New("max_turns must not be negative")
	}
	if meta.Timeout != "" {
		if _, err := time.ParseDuration(meta.Timeout); err != nil {
			return Definition{}, fmt.Errorf("invalid timeout: %w", err)
		}
	}
	tools := normalizeTools(meta.Tools)
	def := Definition{
		Name:        meta.Name,
		Description: strings.TrimSpace(meta.Description),
		Scope:       scope,
		Path:        path,
		Model:       strings.TrimSpace(meta.Model),
		Tools:       tools,
		MaxTurns:    meta.MaxTurns,
		Timeout:     strings.TrimSpace(meta.Timeout),
		Body:        strings.TrimSpace(body),
	}
	def.Ref = definitionRef(scope, projectID, def.Name)
	def.Digest = definitionDigest(def)
	return def, nil
}

func parseDocument(content string) (frontmatter, string, error) {
	content = strings.TrimPrefix(content, "\uFEFF")
	lines := strings.Split(content, "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return frontmatter{}, "", errors.New("missing YAML frontmatter")
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return frontmatter{}, "", errors.New("unterminated YAML frontmatter")
	}
	var meta frontmatter
	if err := yaml.Unmarshal([]byte(strings.Join(lines[1:end], "\n")), &meta); err != nil {
		return frontmatter{}, "", fmt.Errorf("decode frontmatter: %w", err)
	}
	return meta, strings.Join(lines[end+1:], "\n"), nil
}

func normalizeTools(tools []string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(tools))
	for _, name := range tools {
		name = strings.TrimSpace(name)
		if name == "" || isAgentControlTool(name) || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func isAgentControlTool(name string) bool {
	switch name {
	case "agent", "agent_search", "spawn_agent", "wait_agents",
		"read_agent_output", "cancel_agent", "list_agents":
		return true
	default:
		return false
	}
}

func definitionRef(scope Scope, projectID, name string) string {
	if scope == ScopeProject {
		return "project:" + projectID + ":" + name
	}
	return string(scope) + ":" + name
}

func definitionDigest(def Definition) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		def.Name,
		def.Description,
		def.Model,
		strings.Join(def.Tools, "\x00"),
		def.Body,
	}, "\x01")))
	return hex.EncodeToString(sum[:])
}

func scopeRank(scope Scope) int {
	switch scope {
	case ScopeProject:
		return 3
	case ScopeUser:
		return 2
	default:
		return 1
	}
}

func sortDefinitions(items []Definition) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Scope != items[j].Scope {
			return scopeRank(items[i].Scope) > scopeRank(items[j].Scope)
		}
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})
}
