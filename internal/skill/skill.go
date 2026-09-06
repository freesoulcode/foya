// Package skill discovers and loads Agent Skills from trusted local roots.
package skill

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	// Higher-ranked scopes win by name: project > global > plugin > builtin.
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

func scanRoot(root string, scope Scope) (ScanResult, error) {
	info, err := os.Stat(root)
	if errors.Is(err, os.ErrNotExist) {
		return ScanResult{}, nil
	}
	if err != nil {
		return ScanResult{}, err
	}
	if !info.IsDir() {
		return ScanResult{}, nil
	}
	var out []Skill
	var rejected []RejectedSkill
	var diagnostics []Diagnostic
	seen := make(map[string]bool)
	addSkill := func(path string) {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			resolved = path
		}
		if seen[resolved] || len(out) >= maxSkillsPerRoot {
			return
		}
		seen[resolved] = true
		item, itemDiagnostics, err := parseFile(path, scope)
		if err != nil {
			name := filepath.Base(filepath.Dir(path))
			rejectedDiagnostics := []Diagnostic{
				diagnostic("", name, path, "invalid_skill", "error", err.Error(), ""),
			}
			rejected = append(rejected, RejectedSkill{
				Name:        name,
				Path:        path,
				Scope:       scope,
				Diagnostics: rejectedDiagnostics,
			})
			diagnostics = append(diagnostics, rejectedDiagnostics...)
			return
		}
		item.Diagnostics = itemDiagnostics
		diagnostics = append(diagnostics, itemDiagnostics...)
		out = append(out, item)
	}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			target, err := filepath.EvalSymlinks(path)
			if err != nil {
				return nil
			}
			targetInfo, err := os.Stat(target)
			if err != nil {
				return nil
			}
			if targetInfo.IsDir() {
				mainPath := filepath.Join(target, "SKILL.md")
				mainInfo, err := os.Stat(mainPath)
				if err == nil && !mainInfo.IsDir() {
					addSkill(mainPath)
				}
			} else if strings.EqualFold(entry.Name(), "SKILL.md") {
				addSkill(path)
			}
			return nil
		}
		if entry.IsDir() || !strings.EqualFold(entry.Name(), "SKILL.md") {
			return nil
		}
		if len(out) >= maxSkillsPerRoot {
			return filepath.SkipAll
		}
		addSkill(path)
		return nil
	})
	return ScanResult{Skills: out, Inventory: out, Rejected: rejected, Diagnostics: diagnostics}, err
}

func parseFile(path string, scope Scope) (Skill, []Diagnostic, error) {
	file, err := os.Open(path)
	if err != nil {
		return Skill{}, nil, err
	}
	defer file.Close()
	data, err := ioReadAllLimit(file, maxSkillFileBytes)
	if err != nil {
		return Skill{}, nil, err
	}
	meta, body, fields, hasFrontmatter, err := parseDocument(string(data))
	if err != nil {
		return Skill{}, nil, err
	}
	name := strings.TrimSpace(meta.Name)
	if name == "" {
		name = filepath.Base(filepath.Dir(path))
	}
	if name == "" || len([]rune(name)) > 128 {
		return Skill{}, nil, errors.New("skill name is empty or too long")
	}
	root := filepath.Dir(path)
	manifest := SkillManifest{
		Name:                 name,
		Description:          strings.TrimSpace(meta.Description),
		AllowedTools:         compactStrings(meta.AllowedTools),
		RequiredTools:        compactStrings(meta.RequiredTools),
		RequiredCapabilities: compactStrings(meta.RequiredCapabilities),
		License:              strings.TrimSpace(meta.License),
		Compatibility:        strings.TrimSpace(meta.Compatibility),
		Metadata:             compactMap(meta.Metadata),
		Category:             strings.TrimSpace(meta.Category),
	}
	item := Skill{
		Ref:                  skillRef(scope, name),
		Name:                 name,
		Description:          manifest.Description,
		Scope:                scope,
		Path:                 path,
		Root:                 root,
		MainPath:             path,
		Manifest:             manifest,
		Enabled:              true,
		AllowedTools:         manifest.AllowedTools,
		RequiredTools:        manifest.RequiredTools,
		RequiredCapabilities: manifest.RequiredCapabilities,
		Resources:            collectResources(root, path),
		ContentHash:          hashBytes(data),
		Body:                 strings.TrimSpace(body),
	}
	var diagnostics []Diagnostic
	if !hasFrontmatter {
		diagnostics = append(diagnostics, diagnostic(item.Ref, item.Name, path, "missing_frontmatter", "warning", "SKILL.md has no YAML frontmatter", "frontmatter"))
	}
	if item.Description == "" {
		diagnostics = append(diagnostics, diagnostic(item.Ref, item.Name, path, "missing_description", "warning", "SKILL.md has no description", "description"))
	}
	for _, field := range unsupportedManifestFields(fields) {
		diagnostics = append(diagnostics, diagnostic(item.Ref, item.Name, path, "unsupported_field", "warning", "unsupported frontmatter field: "+field, field))
	}
	return item, diagnostics, nil
}

func parseDocument(source string) (SkillManifest, string, map[string]bool, bool, error) {
	if !strings.HasPrefix(source, "---\n") && !strings.HasPrefix(source, "---\r\n") {
		return SkillManifest{}, source, nil, false, nil
	}
	normalized := strings.ReplaceAll(source, "\r\n", "\n")
	end := strings.Index(normalized[4:], "\n---\n")
	if end < 0 {
		return SkillManifest{}, "", nil, true, errors.New("unterminated YAML frontmatter")
	}
	end += 4
	raw := normalized[4:end]
	var meta SkillManifest
	if err := yaml.Unmarshal([]byte(raw), &meta); err != nil {
		return SkillManifest{}, "", nil, true, fmt.Errorf("decode frontmatter: %w", err)
	}
	fields := make(map[string]bool)
	var rawFields map[string]any
	if err := yaml.Unmarshal([]byte(raw), &rawFields); err == nil {
		for key := range rawFields {
			fields[key] = true
		}
	}
	return meta, normalized[end+5:], fields, true, nil
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

func compactMap(values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		if key == "" || value == nil {
			continue
		}
		if text, ok := value.(string); ok {
			value = strings.TrimSpace(text)
			if value == "" {
				continue
			}
		}
		out[key] = value
	}
	return out
}

func completeSkillDefaults(item *Skill, scope Scope) {
	if item.Scope == "" {
		item.Scope = scope
	}
	item.Name = strings.TrimSpace(item.Name)
	item.Description = strings.TrimSpace(item.Description)
	item.AllowedTools = compactStrings(item.AllowedTools)
	item.RequiredTools = compactStrings(item.RequiredTools)
	item.RequiredCapabilities = compactStrings(item.RequiredCapabilities)
	item.Manifest.Name = item.Name
	item.Manifest.Description = item.Description
	item.Manifest.AllowedTools = item.AllowedTools
	item.Manifest.RequiredTools = item.RequiredTools
	item.Manifest.RequiredCapabilities = item.RequiredCapabilities
	if item.Ref == "" && item.Name != "" {
		item.Ref = skillRef(item.Scope, item.Name)
	}
	if item.MainPath == "" {
		item.MainPath = item.Path
	}
	if item.Root == "" && item.MainPath != "" {
		item.Root = filepath.Dir(item.MainPath)
	}
	if item.ContentHash == "" {
		item.ContentHash = hashBytes([]byte(item.Body))
	}
}

func collectResources(root, mainPath string) []Resource {
	var out []Resource
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path == root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if len(out) >= maxSkillResources {
			return filepath.SkipAll
		}
		if samePath(path, mainPath) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		out = append(out, Resource{
			Path:      filepath.ToSlash(rel),
			Size:      info.Size(),
			MediaType: mediaTypeForPath(path),
		})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func samePath(a, b string) bool {
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	return errA == nil && errB == nil && aa == bb
}

func mediaTypeForPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown":
		return "text/markdown"
	case ".txt", ".log":
		return "text/plain"
	case ".json":
		return "application/json"
	case ".yaml", ".yml":
		return "application/yaml"
	case ".sh", ".bash", ".zsh":
		return "text/x-shellscript"
	case ".py":
		return "text/x-python"
	case ".js", ".jsx":
		return "text/javascript"
	case ".ts", ".tsx":
		return "text/typescript"
	case ".go":
		return "text/x-go"
	case ".rs":
		return "text/rust"
	case ".toml":
		return "application/toml"
	case ".html":
		return "text/html"
	case ".css":
		return "text/css"
	default:
		return "application/octet-stream"
	}
}

func readableResource(path string) bool {
	switch mediaTypeForPath(path) {
	case "text/markdown", "text/plain", "application/json", "application/yaml",
		"text/x-shellscript", "text/x-python", "text/javascript",
		"text/typescript", "text/x-go", "text/rust", "application/toml",
		"text/html", "text/css":
		return true
	default:
		return false
	}
}

func cleanResourcePath(path string) (string, error) {
	path = strings.TrimSpace(path)
	path = strings.ReplaceAll(path, "\\", "/")
	if path == "" {
		return "", errors.New("resource path is required")
	}
	if strings.HasPrefix(path, "/") {
		return "", errors.New("resource path must be relative")
	}
	clean := filepath.ToSlash(filepath.Clean(path))
	if clean == "." || clean == "" || strings.HasPrefix(clean, "../") || clean == ".." {
		return "", errors.New("resource path must stay inside the skill package")
	}
	return clean, nil
}

func pathInside(root, child string) bool {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	childAbs, err := filepath.Abs(child)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(rootAbs, childAbs)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..")
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func unsupportedManifestFields(fields map[string]bool) []string {
	if len(fields) == 0 {
		return nil
	}
	allowed := map[string]bool{
		"name": true, "description": true, "allowed-tools": true,
		"required-tools": true, "required-capabilities": true,
		"license": true, "compatibility": true, "metadata": true,
		"category": true,
	}
	var out []string
	for field := range fields {
		if !allowed[field] {
			out = append(out, field)
		}
	}
	sort.Strings(out)
	return out
}

func diagnostic(ref, name, path, code, severity, message, field string) Diagnostic {
	return Diagnostic{
		Ref:      ref,
		Name:     name,
		Path:     path,
		Code:     code,
		Severity: severity,
		Message:  message,
		Field:    field,
	}
}

func sortDiagnostics(items []Diagnostic) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Path != items[j].Path {
			return items[i].Path < items[j].Path
		}
		if items[i].Code != items[j].Code {
			return items[i].Code < items[j].Code
		}
		return items[i].Message < items[j].Message
	})
}

func sortRejected(items []RejectedSkill) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Scope != items[j].Scope {
			return scopeRank(items[i].Scope) > scopeRank(items[j].Scope)
		}
		return items[i].Path < items[j].Path
	})
}

func MissingRequirements(item Skill, availableTools, capabilities map[string]bool) (tools, caps []string) {
	for _, name := range item.RequiredTools {
		if !availableTools[name] {
			tools = append(tools, name)
		}
	}
	for _, name := range item.RequiredCapabilities {
		if !capabilities[name] {
			caps = append(caps, name)
		}
	}
	return tools, caps
}

func IsInvocable(item Skill, availableTools, capabilities map[string]bool) bool {
	tools, caps := MissingRequirements(item, availableTools, capabilities)
	return len(tools) == 0 && len(caps) == 0
}

func skillRef(scope Scope, name string) string {
	return string(scope) + ":" + strings.ToLower(strings.TrimSpace(name))
}

func projectSkillRef(projectID, name string) string {
	return string(ScopeProject) + ":" + projectID + ":" + strings.ToLower(strings.TrimSpace(name))
}

func pluginSkillRef(pluginID, name string) string {
	return string(ScopePlugin) + ":" + pluginID + ":" + strings.ToLower(strings.TrimSpace(name))
}

func scopeRank(scope Scope) int {
	switch scope {
	case ScopeProject:
		return 4
	case ScopeGlobal:
		return 3
	case ScopePlugin:
		return 2
	default:
		return 1
	}
}
