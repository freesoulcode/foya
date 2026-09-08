// Package plugin implements the Agent Plugins 1.0.0 package format.
package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"os"

	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/freesoulcode/foya/internal/mcpclient"
)

const (
	ManifestSchema = "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json"
	MCPSchema      = "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json"

	maxManifestBytes = 64 << 10
	maxMCPBytes      = 256 << 10
	maxPackageFiles  = 2048
	maxPackageBytes  = 32 << 20
	maxPlugins       = 128
)

var (
	ErrNotFound      = errors.New("plugin not found")
	ErrAlreadyExists = errors.New("plugin already exists")
	ErrInvalidSource = errors.New("invalid plugin source")
	ErrInvalidPlugin = errors.New("invalid plugin")
)

var pluginNamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$`)
var githubRepoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
var gitSSHPattern = regexp.MustCompile(`^git@[A-Za-z0-9.-]+:[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+(?:\.git)?$`)

type Author struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
	URL   string `json:"url,omitempty"`
}

type Manifest struct {
	Schema      string                     `json:"$schema"`
	Name        string                     `json:"name"`
	Version     string                     `json:"version,omitempty"`
	Description string                     `json:"description,omitempty"`
	Author      *Author                    `json:"author,omitempty"`
	Homepage    string                     `json:"homepage,omitempty"`
	Repository  string                     `json:"repository,omitempty"`
	License     string                     `json:"license,omitempty"`
	Keywords    []string                   `json:"keywords,omitempty"`
	Extensions  map[string]json.RawMessage `json:"-"`
}

type Diagnostic struct {
	Component string `json:"component,omitempty"`
	Path      string `json:"path,omitempty"`
	Code      string `json:"code"`
	Severity  string `json:"severity"`
	Message   string `json:"message"`
}

type Plugin struct {
	Manifest
	Path           string       `json:"path"`
	DataPath       string       `json:"data_path"`
	Source         string       `json:"source,omitempty"`
	Enabled        bool         `json:"enabled"`
	Valid          bool         `json:"valid"`
	SkillCount     int          `json:"skill_count"`
	MCPServerCount int          `json:"mcp_server_count"`
	Diagnostics    []Diagnostic `json:"diagnostics,omitempty"`
}

type SkillRoot struct {
	PluginID string
	Path     string
}

type persistedState struct {
	Disabled []string          `json:"disabled"`
	Sources  map[string]string `json:"sources,omitempty"`
}

type Manager struct {
	dataDir    string
	root       string
	dataRoot   string
	marketRoot string
	opMu       sync.Mutex
	mu         sync.RWMutex
	disabled   map[string]bool
	sources    map[string]string
	markets    map[string]marketplaceDefinition
}

func NewManager(dataDir, homeDir string) (*Manager, error) {
	base := filepath.Join(dataDir, "agent-plugins")
	if strings.TrimSpace(homeDir) != "" {
		base = filepath.Join(homeDir, ".foya")
	}
	manager := &Manager{
		dataDir:    dataDir,
		root:       filepath.Join(base, "plugins"),
		dataRoot:   filepath.Join(base, "plugin-data"),
		marketRoot: filepath.Join(base, "marketplaces"),
		disabled:   make(map[string]bool),
		sources:    make(map[string]string),
		markets:    make(map[string]marketplaceDefinition),
	}
	if err := manager.loadState(); err != nil {
		return nil, err
	}
	if err := manager.loadMarketplaceState(); err != nil {
		return nil, err
	}
	return manager, nil
}

func (m *Manager) Root() string { return m.root }

func (m *Manager) List() ([]Plugin, error) {
	m.mu.RLock()
	disabled := cloneBoolMap(m.disabled)
	sources := cloneStringMap(m.sources)
	m.mu.RUnlock()

	entries, err := os.ReadDir(m.root)
	if errors.Is(err, os.ErrNotExist) {
		return []Plugin{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]Plugin, 0, len(entries))
	for _, entry := range entries {
		if len(out) >= maxPlugins {
			break
		}
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		item := inspectDirectory(filepath.Join(m.root, entry.Name()), m.dataRoot)
		item.Source = sources[item.Name]
		item.Enabled = item.Valid && !disabled[item.Name]
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *Manager) SkillRoots() []SkillRoot {
	items, err := m.List()
	if err != nil {
		return nil
	}
	roots := make([]SkillRoot, 0, len(items))
	for _, item := range items {
		if !item.Enabled || item.SkillCount == 0 {
			continue
		}
		roots = append(roots, SkillRoot{
			PluginID: item.Name,
			Path:     filepath.Join(item.Path, "skills"),
		})
	}
	return roots
}

func (m *Manager) MCPServers() ([]mcpclient.ServerConfig, error) {
	items, err := m.List()
	if err != nil {
		return nil, err
	}
	var servers []mcpclient.ServerConfig
	for _, item := range items {
		if !item.Enabled {
			continue
		}
		if err := os.MkdirAll(item.DataPath, 0o700); err != nil {
			return nil, err
		}
		dataPath, err := filepath.EvalSymlinks(item.DataPath)
		if err != nil {
			return nil, err
		}
		configs, _ := inspectMCP(item.Path, dataPath, item.Name)
		servers = append(servers, configs...)
	}
	return servers, nil
}

func (m *Manager) SetEnabled(name string, enabled bool) error {
	item, err := m.find(strings.TrimSpace(name))
	if err != nil {
		return err
	}
	if enabled && !item.Valid {
		return fmt.Errorf("%w: plugin %q has manifest errors", ErrInvalidPlugin, name)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if enabled {
		delete(m.disabled, name)
	} else {
		m.disabled[name] = true
	}
	return m.saveStateLocked()
}

func (m *Manager) Install(ctx context.Context, source string, replace bool) (Plugin, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	kind, location, label, err := normalizeSource(source)
	if err != nil {
		return Plugin{}, err
	}
	if err := os.MkdirAll(m.root, 0o700); err != nil {
		return Plugin{}, err
	}

	switch kind {
	case "git":
		temp, err := os.MkdirTemp(m.root, ".download-*")
		if err != nil {
			return Plugin{}, err
		}
		defer os.RemoveAll(temp)
		if err := cloneRepository(ctx, location, temp); err != nil {
			return Plugin{}, err
		}
		return m.installDirectory(temp, label, "", replace)
	case "local":
		if pathInside(m.root, location) {
			return Plugin{}, fmt.Errorf("%w: source cannot be inside the install root", ErrInvalidSource)
		}
		return m.installDirectory(location, label, "", replace)
	default:
		return Plugin{}, ErrInvalidSource
	}
}

func (m *Manager) installDirectory(source, label, expectedName string, replace bool) (Plugin, error) {
	temp, err := os.MkdirTemp(m.root, ".install-*")
	if err != nil {
		return Plugin{}, err
	}
	defer os.RemoveAll(temp)
	if err := copyTree(source, temp); err != nil {
		return Plugin{}, err
	}
	if err := validateTree(temp); err != nil {
		return Plugin{}, err
	}
	item := inspectDirectory(temp, m.dataRoot)
	if !item.Valid {
		return Plugin{}, fmt.Errorf("%w: %s", ErrInvalidPlugin, errorDiagnostics(item.Diagnostics))
	}
	if expectedName != "" && item.Name != expectedName {
		return Plugin{}, fmt.Errorf(
			"%w: marketplace entry %q contains plugin %q",
			ErrInvalidPlugin,
			expectedName,
			item.Name,
		)
	}
	target := filepath.Join(m.root, item.Name)
	if !pathInside(m.root, target) {
		return Plugin{}, ErrInvalidPlugin
	}
	if _, statErr := os.Stat(target); statErr == nil {
		if !replace {
			return Plugin{}, fmt.Errorf("%w: %s", ErrAlreadyExists, item.Name)
		}
		if err := replaceDirectory(temp, target); err != nil {
			return Plugin{}, err
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return Plugin{}, statErr
	} else {
		entries, readErr := os.ReadDir(m.root)
		if readErr != nil {
			return Plugin{}, readErr
		}
		count := 0
		for _, entry := range entries {
			if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
				count++
			}
		}
		if count >= maxPlugins {
			return Plugin{}, fmt.Errorf("%w: maximum installed plugin count reached", ErrInvalidPlugin)
		}
		if err := os.Rename(temp, target); err != nil {
			return Plugin{}, err
		}
	}

	m.mu.Lock()
	delete(m.disabled, item.Name)
	m.sources[item.Name] = label
	err = m.saveStateLocked()
	m.mu.Unlock()
	if err != nil {
		return Plugin{}, err
	}
	return m.find(item.Name)
}

// Remove uninstalls package files. PLUGIN_DATA is deliberately retained.
func (m *Manager) Remove(name string) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	item, err := m.find(strings.TrimSpace(name))
	if err != nil {
		return err
	}
	if !resolvedInside(m.root, item.Path) {
		return ErrInvalidPlugin
	}
	if err := os.RemoveAll(item.Path); err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.disabled, item.Name)
	delete(m.sources, item.Name)
	err = m.saveStateLocked()
	m.mu.Unlock()
	return err
}

func (m *Manager) find(name string) (Plugin, error) {
	items, err := m.List()
	if err != nil {
		return Plugin{}, err
	}
	for _, item := range items {
		if item.Name == name {
			return item, nil
		}
	}
	return Plugin{}, ErrNotFound
}
