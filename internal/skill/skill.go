// Package skill discovers and loads Agent Skills from trusted local roots.
package skill

import (
	"context"

	"encoding/json"
	"errors"
	"fmt"

	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const (
	maxSkillFileBytes     = 256 * 1024
	maxSkillResourceBytes = 512 * 1024
	maxSkillsPerRoot      = 256
	maxSkillResources     = 512
)

type Scope string

const (
	ScopeBuiltin Scope = "builtin"
	ScopePlugin  Scope = "plugin"
	ScopeGlobal  Scope = "global"
	ScopeProject Scope = "project"
)

type PluginRoot struct {
	PluginID string
	Path     string
}

// Skill is one effective SKILL.md definition.
type Skill struct {
	Ref                  string        `json:"ref"`
	Name                 string        `json:"name"`
	Description          string        `json:"description"`
	Scope                Scope         `json:"scope"`
	Path                 string        `json:"path,omitempty"`
	Root                 string        `json:"root,omitempty"`
	MainPath             string        `json:"main_path,omitempty"`
	Manifest             SkillManifest `json:"manifest"`
	Enabled              bool          `json:"enabled"`
	Pinned               bool          `json:"pinned"`
	AllowedTools         []string      `json:"allowed_tools,omitempty"`
	RequiredTools        []string      `json:"required_tools,omitempty"`
	RequiredCapabilities []string      `json:"required_capabilities,omitempty"`
	Resources            []Resource    `json:"resources,omitempty"`
	ContentHash          string        `json:"content_hash,omitempty"`
	Diagnostics          []Diagnostic  `json:"diagnostics,omitempty"`
	Body                 string        `json:"-"`
}

type SkillManifest struct {
	Name                 string         `json:"name,omitempty" yaml:"name"`
	Description          string         `json:"description,omitempty" yaml:"description"`
	AllowedTools         []string       `json:"allowed_tools,omitempty" yaml:"allowed-tools"`
	RequiredTools        []string       `json:"required_tools,omitempty" yaml:"required-tools"`
	RequiredCapabilities []string       `json:"required_capabilities,omitempty" yaml:"required-capabilities"`
	License              string         `json:"license,omitempty" yaml:"license"`
	Compatibility        string         `json:"compatibility,omitempty" yaml:"compatibility"`
	Metadata             map[string]any `json:"metadata,omitempty" yaml:"metadata"`
	Category             string         `json:"category,omitempty" yaml:"category"`
}

type Resource struct {
	Path      string `json:"path"`
	Size      int64  `json:"size,omitempty"`
	MediaType string `json:"media_type,omitempty"`
}

type ResourceContent struct {
	SkillRef  string `json:"skill_ref"`
	Path      string `json:"path"`
	MediaType string `json:"media_type,omitempty"`
	Content   string `json:"content"`
}

type Diagnostic struct {
	Ref      string `json:"ref,omitempty"`
	Name     string `json:"name,omitempty"`
	Path     string `json:"path,omitempty"`
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Field    string `json:"field,omitempty"`
}

type RejectedSkill struct {
	Ref         string       `json:"ref,omitempty"`
	Name        string       `json:"name,omitempty"`
	Path        string       `json:"path,omitempty"`
	Scope       Scope        `json:"scope"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

type ScanResult struct {
	Skills      []Skill         `json:"skills"`
	Inventory   []Skill         `json:"inventory"`
	Rejected    []RejectedSkill `json:"rejected,omitempty"`
	Diagnostics []Diagnostic    `json:"diagnostics,omitempty"`
}

type persistedState struct {
	Disabled []string `json:"disabled"`
	Pinned   []string `json:"pinned"`
}

// Manager owns discovery precedence and persisted enablement state.
type Manager struct {
	dataDir     string
	homeDir     string
	builtins    []Skill
	mu          sync.RWMutex
	disabled    map[string]bool
	pinned      map[string]bool
	pluginRoots func() []PluginRoot
}

func (m *Manager) SetPluginRoots(provider func() []PluginRoot) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pluginRoots = provider
}

func NewManager(dataDir, homeDir string, builtins []Skill) (*Manager, error) {
	m := &Manager{
		dataDir:  dataDir,
		homeDir:  homeDir,
		builtins: append([]Skill(nil), builtins...),
		disabled: make(map[string]bool),
		pinned:   make(map[string]bool),
	}
	for index := range m.builtins {
		completeSkillDefaults(&m.builtins[index], ScopeBuiltin)
	}
	if err := m.loadState(); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manager) List(ctx context.Context, projectID, projectPath string) ([]Skill, error) {
	report, err := m.Inspect(ctx, projectID, projectPath)
	if err != nil {
		return nil, err
	}
	return report.Skills, nil
}

func (m *Manager) Inspect(ctx context.Context, projectID, projectPath string) (ScanResult, error) {
	report, err := m.InspectAll(ctx, projectID, projectPath)
	if err != nil {
		return ScanResult{}, err
	}

	effective := make(map[string]Skill)
	for _, item := range report.Inventory {
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
	report.Skills = out
	return report, nil
}

// ListAll returns every discovered skill scope so management clients can
// present global and project skills separately. Within one scope, .foya wins
// over .agents when both define the same skill name.
func (m *Manager) ListAll(ctx context.Context, projectID, projectPath string) ([]Skill, error) {
	report, err := m.InspectAll(ctx, projectID, projectPath)
	if err != nil {
		return nil, err
	}
	return report.Inventory, nil
}

func (m *Manager) InspectAll(_ context.Context, projectID, projectPath string) (ScanResult, error) {
	all := make([]Skill, 0, len(m.builtins)+16)
	rejected := make([]RejectedSkill, 0)
	diagnostics := make([]Diagnostic, 0)
	for _, item := range m.builtins {
		completeSkillDefaults(&item, ScopeBuiltin)
		all = append(all, item)
	}
	m.mu.RLock()
	pluginRoots := m.pluginRoots
	m.mu.RUnlock()
	if pluginRoots != nil {
		for _, root := range pluginRoots() {
			result := scanPluginRoot(root)
			all = append(all, result.Skills...)
			rejected = append(rejected, result.Rejected...)
			diagnostics = append(diagnostics, result.Diagnostics...)
		}
	}
	if m.homeDir != "" {
		for _, root := range []string{".agents", ".foya"} {
			result, err := scanRoot(filepath.Join(m.homeDir, root, "skills"), ScopeGlobal)
			if err != nil {
				diagnostics = append(diagnostics, diagnostic("", "", filepath.Join(m.homeDir, root, "skills"), "read_failed", "error", err.Error(), ""))
				continue
			}
			all = append(all, result.Skills...)
			rejected = append(rejected, result.Rejected...)
			diagnostics = append(diagnostics, result.Diagnostics...)
		}
	}
	if projectPath != "" {
		if strings.TrimSpace(projectID) == "" {
			return ScanResult{}, errors.New("project ID is required for project skills")
		}
		for _, root := range []string{".agents", ".foya"} {
			result, err := scanRoot(filepath.Join(projectPath, root, "skills"), ScopeProject)
			if err != nil {
				diagnostics = append(diagnostics, diagnostic("", "", filepath.Join(projectPath, root, "skills"), "read_failed", "error", err.Error(), ""))
				continue
			}
			for index := range result.Skills {
				result.Skills[index].Ref = projectSkillRef(projectID, result.Skills[index].Name)
			}
			for index := range result.Rejected {
				if result.Rejected[index].Name != "" {
					result.Rejected[index].Ref = projectSkillRef(projectID, result.Rejected[index].Name)
				}
			}
			all = append(all, result.Skills...)
			rejected = append(rejected, result.Rejected...)
			diagnostics = append(diagnostics, result.Diagnostics...)
		}
	}

	byScopeAndName := make(map[string]Skill)
	for _, item := range all {
		key := string(item.Scope) + ":" + strings.ToLower(item.Name)
		if item.Scope == ScopePlugin {
			key = item.Ref
		}
		byScopeAndName[key] = item
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Skill, 0, len(byScopeAndName))
	for _, item := range byScopeAndName {
		item.Enabled = !m.disabled[item.Ref]
		item.Pinned = m.pinned[item.Ref]
		out = append(out, item)
	}
	sortSkills(out)
	sortRejected(rejected)
	sortDiagnostics(diagnostics)
	return ScanResult{Inventory: out, Rejected: rejected, Diagnostics: diagnostics}, nil
}

func scanPluginRoot(root PluginRoot) ScanResult {
	var result ScanResult
	if strings.TrimSpace(root.PluginID) == "" || strings.TrimSpace(root.Path) == "" {
		return result
	}
	entries, err := os.ReadDir(root.Path)
	if err != nil {
		result.Diagnostics = append(result.Diagnostics, diagnostic(
			"", "", root.Path, "read_failed", "error", err.Error(), "",
		))
		return result
	}
	for _, entry := range entries {
		if !entry.IsDir() || len(result.Skills) >= maxSkillsPerRoot {
			continue
		}
		mainPath := filepath.Join(root.Path, entry.Name(), "SKILL.md")
		info, err := os.Stat(mainPath)
		if err != nil || info.IsDir() {
			continue
		}
		resolvedRoot, rootErr := filepath.EvalSymlinks(root.Path)
		resolvedMain, mainErr := filepath.EvalSymlinks(mainPath)
		if rootErr != nil || mainErr != nil || !pathInside(resolvedRoot, resolvedMain) {
			result.Diagnostics = append(result.Diagnostics, diagnostic(
				"", entry.Name(), mainPath, "path_escape", "warning",
				"plugin skill resolves outside the plugin root", "",
			))
			continue
		}
		item, itemDiagnostics, err := parseFile(mainPath, ScopePlugin)
		ref := pluginSkillRef(root.PluginID, entry.Name())
		if err != nil {
			rejectedDiagnostics := append(itemDiagnostics, diagnostic(
				ref, entry.Name(), mainPath, "parse_failed", "error", err.Error(), "",
			))
			result.Rejected = append(result.Rejected, RejectedSkill{
				Ref: ref, Name: entry.Name(), Path: mainPath,
				Scope: ScopePlugin, Diagnostics: rejectedDiagnostics,
			})
			result.Diagnostics = append(result.Diagnostics, rejectedDiagnostics...)
			continue
		}
		item.Ref = pluginSkillRef(root.PluginID, item.Name)
		for index := range itemDiagnostics {
			itemDiagnostics[index].Ref = item.Ref
		}
		item.Diagnostics = itemDiagnostics
		result.Skills = append(result.Skills, item)
		result.Diagnostics = append(result.Diagnostics, itemDiagnostics...)
	}
	result.Inventory = result.Skills
	return result
}

func sortSkills(items []Skill) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Scope != items[j].Scope {
			return scopeRank(items[i].Scope) > scopeRank(items[j].Scope)
		}
		left := strings.ToLower(items[i].Name)
		right := strings.ToLower(items[j].Name)
		if left != right {
			return left < right
		}
		return items[i].Ref < items[j].Ref
	})
}

func (m *Manager) Get(
	ctx context.Context,
	projectID, projectPath, refOrName string,
) (Skill, error) {
	if strings.Contains(refOrName, ":") {
		items, err := m.ListAll(ctx, projectID, projectPath)
		if err != nil {
			return Skill{}, err
		}
		for _, item := range items {
			if item.Ref == refOrName {
				if !item.Enabled {
					return Skill{}, fmt.Errorf("skill %q is disabled", item.Name)
				}
				return item, nil
			}
		}
	}
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

func (m *Manager) ReadResource(
	ctx context.Context,
	projectID, projectPath, refOrName, relativePath string,
) (ResourceContent, error) {
	item, err := m.Get(ctx, projectID, projectPath, refOrName)
	if err != nil {
		return ResourceContent{}, err
	}
	if item.Root == "" {
		return ResourceContent{}, errors.New("builtin skill does not have package resources")
	}
	cleanPath, err := cleanResourcePath(relativePath)
	if err != nil {
		return ResourceContent{}, err
	}
	if !readableResource(cleanPath) {
		return ResourceContent{}, errors.New("skill resource type is not readable as text")
	}
	root, err := filepath.EvalSymlinks(item.Root)
	if err != nil {
		return ResourceContent{}, fmt.Errorf("resolve skill root: %w", err)
	}
	candidate := filepath.Join(root, filepath.FromSlash(cleanPath))
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return ResourceContent{}, fmt.Errorf("resolve skill resource: %w", err)
	}
	if !pathInside(root, resolved) {
		return ResourceContent{}, errors.New("skill resource escapes package root")
	}
	file, err := os.Open(resolved)
	if err != nil {
		return ResourceContent{}, err
	}
	defer file.Close()
	data, err := ioReadAllLimit(file, maxSkillResourceBytes)
	if err != nil {
		return ResourceContent{}, err
	}
	return ResourceContent{
		SkillRef:  item.Ref,
		Path:      cleanPath,
		MediaType: mediaTypeForPath(cleanPath),
		Content:   string(data),
	}, nil
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

func (m *Manager) SetPinned(ref string, pinned bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if pinned {
		m.pinned[ref] = true
	} else {
		delete(m.pinned, ref)
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
	for _, ref := range state.Pinned {
		if strings.HasPrefix(ref, "user:") {
			ref = "global:" + strings.TrimPrefix(ref, "user:")
		}
		m.pinned[ref] = true
	}
	return nil
}

func (m *Manager) saveStateLocked() error {
	disabled := make([]string, 0, len(m.disabled))
	for ref := range m.disabled {
		disabled = append(disabled, ref)
	}
	pinned := make([]string, 0, len(m.pinned))
	for ref := range m.pinned {
		pinned = append(pinned, ref)
	}
	sort.Strings(disabled)
	sort.Strings(pinned)
	data, err := json.MarshalIndent(persistedState{Disabled: disabled, Pinned: pinned}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(m.dataDir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(m.statePath(), data, 0o600)
}
