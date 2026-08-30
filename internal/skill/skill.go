// Package skill discovers and loads Agent Skills from trusted local roots.
package skill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

const (
	maxSkillFileBytes = 256 * 1024
	maxSkillsPerRoot  = 256
)

type Scope string

const (
	ScopeBuiltin Scope = "builtin"
	ScopeGlobal  Scope = "global"
	ScopeProject Scope = "project"
)

// Skill is one effective SKILL.md definition.
type Skill struct {
	Ref          string   `json:"ref"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Scope        Scope    `json:"scope"`
	Path         string   `json:"path,omitempty"`
	Enabled      bool     `json:"enabled"`
	AllowedTools []string `json:"allowed_tools,omitempty"`
	Body         string   `json:"-"`
}

type frontmatter struct {
	Name         string   `yaml:"name"`
	Description  string   `yaml:"description"`
	AllowedTools []string `yaml:"allowed-tools"`
}

type persistedState struct {
	Disabled []string `json:"disabled"`
}

// Manager owns discovery precedence and persisted enablement state.
type Manager struct {
	dataDir  string
	homeDir  string
	builtins []Skill
	mu       sync.RWMutex
	disabled map[string]bool
}

func NewManager(dataDir, homeDir string, builtins []Skill) (*Manager, error) {
	m := &Manager{
		dataDir:  dataDir,
		homeDir:  homeDir,
		builtins: append([]Skill(nil), builtins...),
		disabled: make(map[string]bool),
	}
	if err := m.loadState(); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manager) List(ctx context.Context, projectID, projectPath string) ([]Skill, error) {
	all, err := m.ListAll(ctx, projectID, projectPath)
	if err != nil {
		return nil, err
	}

	// Higher-ranked scopes win by name: project > global > builtin.
	effective := make(map[string]Skill)
	for _, item := range all {
		key := strings.ToLower(item.Name)
		current, exists := effective[key]
		if !exists || scopeRank(item.Scope) > scopeRank(current.Scope) {
			effective[key] = item
		}
	}
	out := make([]Skill, 0, len(effective))
	for _, item := range effective {
		out = append(out, item)
	}
	sortSkills(out)
	return out, nil
}

// ListAll returns every discovered skill scope so management clients can
// present global and project skills separately. Within one scope, .foya wins
// over .agents when both define the same skill name.
func (m *Manager) ListAll(_ context.Context, projectID, projectPath string) ([]Skill, error) {
	all := make([]Skill, 0, len(m.builtins)+16)
	for _, item := range m.builtins {
		item.Scope = ScopeBuiltin
		item.Ref = skillRef(item.Scope, item.Name)
		all = append(all, item)
	}
	if m.homeDir != "" {
		for _, root := range []string{".agents", ".foya"} {
			items, err := scanRoot(filepath.Join(m.homeDir, root, "skills"), ScopeGlobal)
			if err != nil {
				return nil, err
			}
			all = append(all, items...)
		}
	}
	if projectPath != "" {
		if strings.TrimSpace(projectID) == "" {
			return nil, errors.New("project ID is required for project skills")
		}
		for _, root := range []string{".agents", ".foya"} {
			items, err := scanRoot(filepath.Join(projectPath, root, "skills"), ScopeProject)
			if err != nil {
				return nil, err
			}
			for index := range items {
				items[index].Ref = projectSkillRef(projectID, items[index].Name)
			}
			all = append(all, items...)
		}
	}

	byScopeAndName := make(map[string]Skill)
	for _, item := range all {
		key := string(item.Scope) + ":" + strings.ToLower(item.Name)
		byScopeAndName[key] = item
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Skill, 0, len(byScopeAndName))
	for _, item := range byScopeAndName {
		item.Enabled = !m.disabled[item.Ref]
		out = append(out, item)
	}
	sortSkills(out)
	return out, nil
}

func sortSkills(items []Skill) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Scope != items[j].Scope {
			return scopeRank(items[i].Scope) > scopeRank(items[j].Scope)
		}
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})
}

func (m *Manager) Get(
	ctx context.Context,
	projectID, projectPath, refOrName string,
) (Skill, error) {
	items, err := m.List(ctx, projectID, projectPath)
	if err != nil {
		return Skill{}, err
	}
	for _, item := range items {
		if item.Ref == refOrName || strings.EqualFold(item.Name, refOrName) {
			if !item.Enabled {
				return Skill{}, fmt.Errorf("skill %q is disabled", item.Name)
			}
			return item, nil
		}
	}
	return Skill{}, fs.ErrNotExist
}

func (m *Manager) SetEnabled(ref string, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if enabled {
		delete(m.disabled, ref)
	} else {
		m.disabled[ref] = true
	}
	return m.saveStateLocked()
}

func (m *Manager) statePath() string {
	return filepath.Join(m.dataDir, "skills.json")
}

func (m *Manager) loadState() error {
	data, err := os.ReadFile(m.statePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("decode skills state: %w", err)
	}
	for _, ref := range state.Disabled {
		if strings.HasPrefix(ref, "user:") {
			ref = "global:" + strings.TrimPrefix(ref, "user:")
		}
		m.disabled[ref] = true
	}
	return nil
}

func (m *Manager) saveStateLocked() error {
	disabled := make([]string, 0, len(m.disabled))
	for ref := range m.disabled {
		disabled = append(disabled, ref)
	}
	sort.Strings(disabled)
	data, err := json.MarshalIndent(persistedState{Disabled: disabled}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(m.dataDir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(m.statePath(), data, 0o600)
}

func scanRoot(root string, scope Scope) ([]Skill, error) {
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
	var out []Skill
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() || !strings.EqualFold(entry.Name(), "SKILL.md") {
			return nil
		}
		if len(out) >= maxSkillsPerRoot {
			return filepath.SkipAll
		}
		item, err := parseFile(path, scope)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		out = append(out, item)
		return nil
	})
	return out, err
}

func parseFile(path string, scope Scope) (Skill, error) {
	file, err := os.Open(path)
	if err != nil {
		return Skill{}, err
	}
	defer file.Close()
	data, err := ioReadAllLimit(file, maxSkillFileBytes)
	if err != nil {
		return Skill{}, err
	}
	meta, body, err := parseDocument(string(data))
	if err != nil {
		return Skill{}, err
	}
	name := strings.TrimSpace(meta.Name)
	if name == "" {
		name = filepath.Base(filepath.Dir(path))
	}
	if name == "" || len([]rune(name)) > 128 {
		return Skill{}, errors.New("skill name is empty or too long")
	}
	return Skill{
		Ref:          skillRef(scope, name),
		Name:         name,
		Description:  strings.TrimSpace(meta.Description),
		Scope:        scope,
		Path:         path,
		Enabled:      true,
		AllowedTools: compactStrings(meta.AllowedTools),
		Body:         strings.TrimSpace(body),
	}, nil
}

func parseDocument(source string) (frontmatter, string, error) {
	if !strings.HasPrefix(source, "---\n") && !strings.HasPrefix(source, "---\r\n") {
		return frontmatter{}, source, nil
	}
	normalized := strings.ReplaceAll(source, "\r\n", "\n")
	end := strings.Index(normalized[4:], "\n---\n")
	if end < 0 {
		return frontmatter{}, "", errors.New("unterminated YAML frontmatter")
	}
	end += 4
	var meta frontmatter
	if err := yaml.Unmarshal([]byte(normalized[4:end]), &meta); err != nil {
		return frontmatter{}, "", fmt.Errorf("decode frontmatter: %w", err)
	}
	return meta, normalized[end+5:], nil
}

func ioReadAllLimit(file *os.File, max int64) ([]byte, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > max {
		return nil, fmt.Errorf("skill file exceeds %d bytes", max)
	}
	return io.ReadAll(io.LimitReader(file, max+1))
}

func compactStrings(values []string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func skillRef(scope Scope, name string) string {
	return string(scope) + ":" + strings.ToLower(strings.TrimSpace(name))
}

func projectSkillRef(projectID, name string) string {
	return string(ScopeProject) + ":" + projectID + ":" + strings.ToLower(strings.TrimSpace(name))
}

func scopeRank(scope Scope) int {
	switch scope {
	case ScopeProject:
		return 3
	case ScopeGlobal:
		return 2
	default:
		return 1
	}
}
