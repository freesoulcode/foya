// Package plugin implements the Agent Plugins 1.0.0 package format.
package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

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

func inspectDirectory(root, dataRoot string) Plugin {
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	if absolute, err := filepath.Abs(dataRoot); err == nil {
		dataRoot = absolute
	}
	fallback := filepath.Base(root)
	item := Plugin{
		Manifest: Manifest{Name: fallback},
		Path:     root,
		DataPath: filepath.Join(dataRoot, fallback),
	}
	manifest, diagnostics := loadManifest(root)
	item.Manifest = manifest
	if item.Name == "" {
		item.Name = fallback
	}
	item.DataPath = filepath.Join(dataRoot, item.Name)
	item.Diagnostics = append(item.Diagnostics, diagnostics...)
	if hasErrors(diagnostics) {
		return item
	}
	item.Valid = true
	item.SkillCount, diagnostics = inspectSkills(root)
	item.Diagnostics = append(item.Diagnostics, diagnostics...)
	servers, diagnostics := inspectMCP(root, item.DataPath, item.Name)
	item.MCPServerCount = len(servers)
	item.Diagnostics = append(item.Diagnostics, diagnostics...)
	return item
}

func loadManifest(root string) (Manifest, []Diagnostic) {
	path := filepath.Join(root, "plugin.json")
	if !resolvedInside(root, path) {
		return Manifest{}, []Diagnostic{diag("manifest", path, "path_escape", "error", "plugin.json resolves outside the plugin root")}
	}
	data, err := readFileLimit(path, maxManifestBytes)
	if err != nil {
		return Manifest{}, []Diagnostic{diag("manifest", path, "manifest_unreadable", "error", err.Error())}
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return Manifest{}, []Diagnostic{diag("manifest", path, "manifest_invalid", "error", err.Error())}
	}
	if raw == nil {
		return Manifest{}, []Diagnostic{diag("manifest", path, "manifest_invalid", "error", "plugin.json must contain an object")}
	}
	allowed := map[string]bool{
		"$schema": true, "name": true, "version": true, "description": true,
		"author": true, "homepage": true, "repository": true, "license": true,
		"keywords": true, "extensions": true,
	}
	var diagnostics []Diagnostic
	for key := range raw {
		if !allowed[key] {
			diagnostics = append(diagnostics, diag("manifest", path, "unknown_field", "warning", "unknown field ignored: "+key))
			delete(raw, key)
		}
	}
	for _, field := range []string{
		"$schema", "name", "version", "description", "homepage", "repository", "license",
	} {
		if value, ok := raw[field]; ok && !jsonString(value) {
			diagnostics = append(diagnostics, diag("manifest", path, "invalid_field", "error", field+" must be a string"))
		}
	}
	if value, ok := raw["keywords"]; ok {
		var keywords []string
		if isJSONNull(value) || json.Unmarshal(value, &keywords) != nil {
			diagnostics = append(diagnostics, diag("manifest", path, "invalid_keywords", "error", "keywords must be an array of strings"))
		}
	}
	if value, ok := raw["extensions"]; ok && !jsonObject(value) {
		diagnostics = append(diagnostics, diag("manifest", path, "invalid_extensions", "warning", "non-object extensions field ignored"))
		delete(raw, "extensions")
	} else if ok {
		var extensions map[string]json.RawMessage
		_ = json.Unmarshal(value, &extensions)
		for namespace, extension := range extensions {
			if !jsonObject(extension) {
				diagnostics = append(diagnostics, diag("manifest", path, "invalid_extension", "error", "extension must be an object: "+namespace))
			}
		}
	}
	clean, _ := json.Marshal(raw)
	var manifest Manifest
	if err := json.Unmarshal(clean, &manifest); err != nil {
		return Manifest{}, append(diagnostics, diag("manifest", path, "manifest_invalid", "error", err.Error()))
	}
	if manifest.Schema != ManifestSchema {
		diagnostics = append(diagnostics, diag("manifest", path, "unsupported_schema", "error", "unsupported or missing $schema"))
	}
	if !validPluginName(manifest.Name) {
		diagnostics = append(diagnostics, diag("manifest", path, "invalid_name", "error", "name does not satisfy Agent Plugins 1.0"))
	}
	if rawAuthor, ok := raw["author"]; ok {
		var authorFields map[string]json.RawMessage
		if json.Unmarshal(rawAuthor, &authorFields) != nil || authorFields == nil {
			diagnostics = append(diagnostics, diag("manifest", path, "invalid_author", "error", "author must be an object"))
		} else {
			for key := range authorFields {
				if key != "name" && key != "email" && key != "url" {
					diagnostics = append(diagnostics, diag("manifest", path, "invalid_author", "error", "unknown author field: "+key))
				}
			}
			for key, value := range authorFields {
				if !jsonString(value) {
					diagnostics = append(diagnostics, diag("manifest", path, "invalid_author", "error", "author."+key+" must be a string"))
				}
			}
		}
	}
	return manifest, diagnostics
}

func inspectSkills(root string) (int, []Diagnostic) {
	skillsRoot := filepath.Join(root, "skills")
	info, err := os.Lstat(skillsRoot)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, []Diagnostic{diag("skills", skillsRoot, "skills_unreadable", "error", err.Error())}
	}
	if !info.IsDir() || !resolvedInside(root, skillsRoot) {
		return 0, []Diagnostic{diag("skills", skillsRoot, "invalid_skills_root", "error", "skills must be a directory inside the plugin root")}
	}
	entries, err := os.ReadDir(skillsRoot)
	if err != nil {
		return 0, []Diagnostic{diag("skills", skillsRoot, "skills_unreadable", "error", err.Error())}
	}
	count := 0
	var diagnostics []Diagnostic
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		mainPath := filepath.Join(skillsRoot, entry.Name(), "SKILL.md")
		mainInfo, err := os.Stat(mainPath)
		if err != nil || mainInfo.IsDir() {
			continue
		}
		if !resolvedInside(root, mainPath) {
			diagnostics = append(diagnostics, diag("skills", mainPath, "skill_path_escape", "warning", "skill skipped because SKILL.md resolves outside the plugin root"))
			continue
		}
		count++
	}
	return count, diagnostics
}

type mcpDocument struct {
	Schema     string                     `json:"$schema"`
	MCPServers map[string]json.RawMessage `json:"mcpServers"`
}

func inspectMCP(root, dataRoot, pluginID string) ([]mcpclient.ServerConfig, []Diagnostic) {
	path := filepath.Join(root, "mcp.json")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil || !info.Mode().IsRegular() || !resolvedInside(root, path) {
		return nil, []Diagnostic{diag("mcp", path, "invalid_mcp_document", "error", "mcp.json must be a regular file inside the plugin root")}
	}
	data, err := readFileLimit(path, maxMCPBytes)
	if err != nil {
		return nil, []Diagnostic{diag("mcp", path, "mcp_unreadable", "error", err.Error())}
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil || raw == nil {
		return nil, []Diagnostic{diag("mcp", path, "mcp_invalid", "error", "mcp.json must contain a JSON object")}
	}
	if len(raw) != 2 || raw["$schema"] == nil || raw["mcpServers"] == nil {
		return nil, []Diagnostic{diag("mcp", path, "mcp_invalid", "error", "mcp.json may contain only $schema and mcpServers")}
	}
	var document mcpDocument
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, []Diagnostic{diag("mcp", path, "mcp_invalid", "error", err.Error())}
	}
	if document.Schema != MCPSchema || document.MCPServers == nil {
		return nil, []Diagnostic{diag("mcp", path, "unsupported_mcp_schema", "error", "unsupported schema or missing mcpServers")}
	}
	names := make([]string, 0, len(document.MCPServers))
	for name := range document.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)
	var out []mcpclient.ServerConfig
	var diagnostics []Diagnostic
	for _, name := range names {
		config, err := decodeMCPServer(document.MCPServers[name], root, dataRoot, pluginID, name)
		if err != nil {
			diagnostics = append(diagnostics, diag("mcp:"+name, path, "invalid_mcp_server", "warning", err.Error()))
			continue
		}
		out = append(out, config)
	}
	return out, diagnostics
}

func decodeMCPServer(raw json.RawMessage, root, dataRoot, pluginID, name string) (mcpclient.ServerConfig, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return mcpclient.ServerConfig{}, errors.New("server must be an object")
	}
	var transport string
	if err := json.Unmarshal(object["type"], &transport); err != nil || transport == "" {
		return mcpclient.ServerConfig{}, errors.New("server type is required")
	}
	config := mcpclient.ServerConfig{
		ID:            "plugin:" + pluginID + ":" + name,
		Name:          pluginID + "/" + name,
		Enabled:       true,
		PluginID:      pluginID,
		LiteralValues: true,
	}
	switch transport {
	case "stdio":
		if err := rejectUnknown(object, "type", "command", "args", "env", "cwd"); err != nil {
			return config, err
		}
		var command string
		if err := json.Unmarshal(object["command"], &command); err != nil || strings.TrimSpace(command) == "" {
			return config, errors.New("stdio command is required")
		}
		command = strings.TrimSpace(command)
		if strings.HasPrefix(command, "./") {
			resolved, err := packagePath(root, command)
			if err != nil {
				return config, fmt.Errorf("command: %w", err)
			}
			command = resolved
		} else if filepath.IsAbs(command) || strings.ContainsAny(command, `/\`) {
			return config, errors.New("command must be a bare executable or start with ./")
		}
		config.Transport = "stdio"
		config.Command = command
		config.Cwd = root
		if value := object["args"]; value != nil {
			if isJSONNull(value) || json.Unmarshal(value, &config.Args) != nil {
				return config, errors.New("args must be an array of strings")
			}
			for index := range config.Args {
				config.Args[index] = expandPluginVariables(config.Args[index], root, dataRoot)
			}
		}
		if value := object["env"]; value != nil {
			if isJSONNull(value) || json.Unmarshal(value, &config.Env) != nil {
				return config, errors.New("env must contain string values")
			}
		}
		if config.Env == nil {
			config.Env = make(map[string]string)
		}
		if _, ok := config.Env["PLUGIN_ROOT"]; ok {
			return config, errors.New("env cannot override PLUGIN_ROOT")
		}
		if _, ok := config.Env["PLUGIN_DATA"]; ok {
			return config, errors.New("env cannot override PLUGIN_DATA")
		}
		for key, value := range config.Env {
			config.Env[key] = expandPluginVariables(value, root, dataRoot)
		}
		config.Env["PLUGIN_ROOT"] = root
		config.Env["PLUGIN_DATA"] = dataRoot
		if value := object["cwd"]; value != nil {
			var cwd string
			if !jsonString(value) || json.Unmarshal(value, &cwd) != nil {
				return config, errors.New("cwd must be a string")
			}
			resolved, err := pluginWorkingDirectory(cwd, root, dataRoot)
			if err != nil {
				return config, err
			}
			config.Cwd = resolved
		}
	case "streamable-http", "sse":
		if err := rejectUnknown(object, "type", "url", "headers"); err != nil {
			return config, err
		}
		var endpoint string
		if err := json.Unmarshal(object["url"], &endpoint); err != nil {
			return config, errors.New("url is required")
		}
		if err := validateRemoteURL(endpoint); err != nil {
			return config, err
		}
		config.Transport = strings.ReplaceAll(transport, "-", "_")
		config.URL = endpoint
		if value := object["headers"]; value != nil {
			if isJSONNull(value) || json.Unmarshal(value, &config.Headers) != nil {
				return config, errors.New("headers must contain string values")
			}
		}
	default:
		return config, fmt.Errorf("unsupported transport %q", transport)
	}
	return config, nil
}

func pluginWorkingDirectory(value, root, dataRoot string) (string, error) {
	switch {
	case strings.HasPrefix(value, "./"):
		return packagePath(root, value)
	case value == "${PLUGIN_ROOT}" || strings.HasPrefix(value, "${PLUGIN_ROOT}/"):
		return rootedPath(root, strings.TrimPrefix(value, "${PLUGIN_ROOT}"))
	case value == "${PLUGIN_DATA}" || strings.HasPrefix(value, "${PLUGIN_DATA}/"):
		return rootedPath(dataRoot, strings.TrimPrefix(value, "${PLUGIN_DATA}"))
	default:
		return "", errors.New("cwd must start with ./, ${PLUGIN_ROOT}, or ${PLUGIN_DATA}")
	}
}

func packagePath(root, value string) (string, error) {
	if !strings.HasPrefix(value, "./") {
		return "", errors.New("plugin-relative path must start with ./")
	}
	return rootedPath(root, strings.TrimPrefix(value, "./"))
}

func rootedPath(root, relative string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(rootAbs, filepath.FromSlash(strings.TrimPrefix(relative, "/")))
	if !pathInside(rootAbs, candidate) {
		return "", errors.New("path escapes its permitted root")
	}
	if resolved, err := filepath.EvalSymlinks(candidate); err == nil && !pathInside(rootAbs, resolved) {
		return "", errors.New("path resolves outside its permitted root")
	}
	return candidate, nil
}

func expandPluginVariables(value, root, dataRoot string) string {
	value = strings.ReplaceAll(value, "${PLUGIN_ROOT}", root)
	return strings.ReplaceAll(value, "${PLUGIN_DATA}", dataRoot)
}

func validateRemoteURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return errors.New("url must be an absolute HTTP URL without user info or fragment")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("url must use HTTP or HTTPS")
	}
	host := strings.Trim(parsed.Hostname(), "[]")
	loopback := strings.EqualFold(host, "localhost")
	if ip := net.ParseIP(host); ip != nil {
		loopback = ip.IsLoopback()
	}
	if !loopback && parsed.Scheme != "https" {
		return errors.New("non-loopback MCP endpoints must use HTTPS")
	}
	return nil
}

func rejectUnknown(values map[string]json.RawMessage, allowed ...string) error {
	set := make(map[string]bool, len(allowed))
	for _, key := range allowed {
		set[key] = true
	}
	for key := range values {
		if !set[key] {
			return fmt.Errorf("unknown field %q", key)
		}
	}
	return nil
}

func (m *Manager) statePath() string { return filepath.Join(m.dataDir, "plugins.json") }

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
		return fmt.Errorf("decode plugin state: %w", err)
	}
	for _, name := range state.Disabled {
		if validPluginName(name) {
			m.disabled[name] = true
		}
	}
	for name, source := range state.Sources {
		if validPluginName(name) {
			m.sources[name] = source
		}
	}
	return nil
}

func (m *Manager) saveStateLocked() error {
	disabled := make([]string, 0, len(m.disabled))
	for name := range m.disabled {
		disabled = append(disabled, name)
	}
	sort.Strings(disabled)
	data, err := json.MarshalIndent(persistedState{
		Disabled: disabled,
		Sources:  cloneStringMap(m.sources),
	}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(m.dataDir, 0o700); err != nil {
		return err
	}
	temp := m.statePath() + ".tmp"
	if err := os.WriteFile(temp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temp, m.statePath()); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}

func normalizeSource(raw string) (kind, location, label string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", "", ErrInvalidSource
	}
	if !strings.Contains(raw, "://") {
		path, pathErr := filepath.Abs(raw)
		if pathErr == nil {
			if info, statErr := os.Stat(path); statErr == nil && info.IsDir() {
				return "local", path, path, nil
			}
		}
	}
	if githubRepoPattern.MatchString(raw) {
		return "git", "https://github.com/" + raw + ".git", raw, nil
	}
	if gitSSHPattern.MatchString(raw) {
		return "git", raw, raw, nil
	}
	if parsed, parseErr := url.Parse(raw); parseErr == nil && parsed.Scheme != "" {
		if parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
			parsed.RawQuery != "" || parsed.Fragment != "" {
			return "", "", "", fmt.Errorf("%w: only credential-free HTTPS Git URLs are allowed", ErrInvalidSource)
		}
		return "git", raw, raw, nil
	}
	return "", "", "", fmt.Errorf(
		"%w: expected a local directory, owner/repo, HTTPS Git URL, or Git SSH URL",
		ErrInvalidSource,
	)
}

func cloneRepository(ctx context.Context, source, destination string) error {
	cloneCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(cloneCtx, "git", "clone", "--depth", "1", "--", source, destination)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("clone plugin: %s", message)
	}
	return nil
}

func copyTree(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		if entry.Name() == ".git" && entry.IsDir() {
			return filepath.SkipDir
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: symbolic links are not accepted during installation", ErrInvalidPlugin)
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%w: package contains a non-regular file", ErrInvalidPlugin)
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(source, destination string, mode fs.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode.Perm()&0o700)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func validateTree(root string) error {
	var files int
	var bytes int64
	return filepath.WalkDir(root, func(_ string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: symbolic links are not accepted during installation", ErrInvalidPlugin)
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		files++
		bytes += info.Size()
		if files > maxPackageFiles || bytes > maxPackageBytes {
			return fmt.Errorf("%w: package exceeds installation limits", ErrInvalidPlugin)
		}
		return nil
	})
}

func replaceDirectory(source, target string) error {
	backup := target + ".backup"
	_ = os.RemoveAll(backup)
	if err := os.Rename(target, backup); err != nil {
		return err
	}
	if err := os.Rename(source, target); err != nil {
		_ = os.Rename(backup, target)
		return err
	}
	return os.RemoveAll(backup)
}

func readFileLimit(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("file is not regular or exceeds size limit")
	}
	return io.ReadAll(io.LimitReader(file, limit+1))
}

func jsonObject(value json.RawMessage) bool {
	var object map[string]json.RawMessage
	return json.Unmarshal(value, &object) == nil && object != nil
}

func jsonString(value json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(value))
	if len(trimmed) == 0 || trimmed[0] != '"' {
		return false
	}
	var text string
	return json.Unmarshal([]byte(trimmed), &text) == nil
}

func isJSONNull(value json.RawMessage) bool {
	return strings.TrimSpace(string(value)) == "null"
}

func resolvedInside(root, path string) bool {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	return pathInside(resolvedRoot, resolvedPath)
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
	relative, err := filepath.Rel(rootAbs, childAbs)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func validPluginName(value string) bool {
	return len(value) >= 1 && len(value) <= 64 &&
		!strings.Contains(value, "--") && !strings.Contains(value, "..") &&
		pluginNamePattern.MatchString(value)
}

func hasErrors(items []Diagnostic) bool {
	for _, item := range items {
		if item.Severity == "error" {
			return true
		}
	}
	return false
}

func errorDiagnostics(items []Diagnostic) string {
	var messages []string
	for _, item := range items {
		if item.Severity == "error" {
			messages = append(messages, item.Message)
		}
	}
	return strings.Join(messages, "; ")
}

func diag(component, path, code, severity, message string) Diagnostic {
	return Diagnostic{Component: component, Path: path, Code: code, Severity: severity, Message: message}
}

func cloneBoolMap(input map[string]bool) map[string]bool {
	out := make(map[string]bool, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func cloneStringMap(input map[string]string) map[string]string {
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}
