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
)

const (
	maxMarketplaceBytes   = 4 << 20
	maxMarketplacePlugins = 2048
)

var (
	ErrMarketplaceNotFound       = errors.New("plugin marketplace not found")
	ErrMarketplaceAlreadyExists  = errors.New("plugin marketplace already exists")
	ErrMarketplaceDisabled       = errors.New("plugin marketplace is disabled")
	ErrMarketplacePluginNotFound = errors.New("marketplace plugin not found")
	gitRevisionPattern           = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,199}$`)
	gitCommitPattern             = regexp.MustCompile(`^(?:[0-9a-fA-F]{40}|[0-9a-fA-F]{64})$`)
)

type MarketplaceRegistrationInput struct {
	Source      string   `json:"source"`
	Ref         string   `json:"ref,omitempty"`
	SparsePaths []string `json:"sparse_paths,omitempty"`
}

type MarketplaceSummary struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Repository  string   `json:"repository"`
	Owner       Author   `json:"owner"`
	Source      string   `json:"source"`
	Ref         string   `json:"ref,omitempty"`
	SparsePaths []string `json:"sparse_paths,omitempty"`
	ResolvedSHA string   `json:"resolved_sha,omitempty"`
	Format      string   `json:"format"`
	Enabled     bool     `json:"enabled"`
}

type MarketplaceCatalog struct {
	MarketplaceSummary
	Plugins []MarketplacePlugin `json:"plugins"`
}

type MarketplacePlugin struct {
	Name             string   `json:"name"`
	Description      string   `json:"description,omitempty"`
	Version          string   `json:"version,omitempty"`
	Author           *Author  `json:"author,omitempty"`
	Homepage         string   `json:"homepage,omitempty"`
	Repository       string   `json:"repository,omitempty"`
	License          string   `json:"license,omitempty"`
	Keywords         []string `json:"keywords,omitempty"`
	Category         string   `json:"category,omitempty"`
	Tags             []string `json:"tags,omitempty"`
	Source           string   `json:"source"`
	Installable      bool     `json:"installable"`
	Reason           string   `json:"reason,omitempty"`
	Installed        bool     `json:"installed"`
	Enabled          bool     `json:"enabled,omitempty"`
	InstalledVersion string   `json:"installed_version,omitempty"`
}

type MarketplacePluginPreview struct {
	Name                  string       `json:"name"`
	Valid                 bool         `json:"valid"`
	Compatibility         string       `json:"compatibility"`
	SkillCount            int          `json:"skill_count"`
	MCPServerCount        int          `json:"mcp_server_count"`
	UnsupportedComponents []string     `json:"unsupported_components,omitempty"`
	Diagnostics           []Diagnostic `json:"diagnostics,omitempty"`
}

type marketplaceDefinition struct {
	ID          string
	Name        string
	Description string
	Repository  string
	Owner       Author
	Source      string
	SourceKind  string
	Location    string
	Ref         string
	SparsePaths []string
	ResolvedSHA string
	Format      string
	Enabled     bool
	RootPath    string
	CatalogPath string
	PluginRoot  string
}

type persistedMarketplaces struct {
	Version int                    `json:"version"`
	Items   []persistedMarketplace `json:"items"`
}

type persistedMarketplace struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Repository  string   `json:"repository"`
	Owner       Author   `json:"owner"`
	Source      string   `json:"source"`
	Ref         string   `json:"ref,omitempty"`
	SparsePaths []string `json:"sparse_paths,omitempty"`
	ResolvedSHA string   `json:"resolved_sha,omitempty"`
	Format      string   `json:"format"`
	Enabled     bool     `json:"enabled"`
}

type marketplaceDocument struct {
	Name     string                      `json:"name"`
	Metadata marketplaceMetadata         `json:"metadata"`
	Owner    Author                      `json:"owner"`
	Plugins  []marketplacePluginDocument `json:"plugins"`
}

type marketplaceMetadata struct {
	Description string `json:"description"`
	Version     string `json:"version"`
	PluginRoot  string `json:"pluginRoot"`
}

type marketplacePluginDocument struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Version     string          `json:"version,omitempty"`
	Author      *Author         `json:"author,omitempty"`
	Homepage    string          `json:"homepage,omitempty"`
	Repository  string          `json:"repository,omitempty"`
	License     string          `json:"license,omitempty"`
	Keywords    []string        `json:"keywords,omitempty"`
	Category    string          `json:"category,omitempty"`
	Tags        []string        `json:"tags,omitempty"`
	Source      json.RawMessage `json:"source"`
}

type marketplaceSourceObject struct {
	Source  string `json:"source"`
	Repo    string `json:"repo,omitempty"`
	URL     string `json:"url,omitempty"`
	Path    string `json:"path,omitempty"`
	Ref     string `json:"ref,omitempty"`
	SHA     string `json:"sha,omitempty"`
	Package string `json:"package,omitempty"`
}

type resolvedMarketplaceSource struct {
	cloneURL   string
	repository string
	localPath  string
	path       string
	revision   string
}

type marketplaceManifestLocation struct {
	Format   string
	Relative string
}

var marketplaceManifestLocations = []marketplaceManifestLocation{
	{Format: "codex", Relative: filepath.Join(".agents", "plugins", "marketplace.json")},
	{Format: "claude", Relative: filepath.Join(".claude-plugin", "marketplace.json")},
	{Format: "copilot", Relative: filepath.Join(".github", "plugin", "marketplace.json")},
}

func (m *Manager) Marketplaces() []MarketplaceSummary {
	m.mu.RLock()
	out := make([]MarketplaceSummary, 0, len(m.markets))
	for _, item := range m.markets {
		out = append(out, marketplaceSummary(item))
	}
	m.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (m *Manager) AddMarketplace(
	ctx context.Context,
	input MarketplaceRegistrationInput,
) (MarketplaceSummary, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	staged, definition, err := m.stageMarketplace(ctx, input)
	if err != nil {
		return MarketplaceSummary{}, err
	}
	defer os.RemoveAll(staged)

	m.mu.RLock()
	_, exists := m.markets[definition.ID]
	m.mu.RUnlock()
	if exists {
		return MarketplaceSummary{}, fmt.Errorf(
			"%w: %s",
			ErrMarketplaceAlreadyExists,
			definition.ID,
		)
	}
	target := filepath.Join(m.marketRoot, definition.ID)
	if _, err := os.Stat(target); err == nil {
		return MarketplaceSummary{}, fmt.Errorf(
			"%w: %s",
			ErrMarketplaceAlreadyExists,
			definition.ID,
		)
	} else if !errors.Is(err, os.ErrNotExist) {
		return MarketplaceSummary{}, err
	}
	if err := os.Rename(staged, target); err != nil {
		return MarketplaceSummary{}, err
	}
	definition.RootPath = target
	definition.CatalogPath = marketplaceCatalogPath(target, definition.Format)

	m.mu.Lock()
	m.markets[definition.ID] = definition
	err = m.saveMarketplaceStateLocked()
	m.mu.Unlock()
	if err != nil {
		_ = os.RemoveAll(target)
		return MarketplaceSummary{}, err
	}
	return marketplaceSummary(definition), nil
}

func (m *Manager) RefreshMarketplace(
	ctx context.Context,
	id string,
) (MarketplaceSummary, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	current, err := m.marketplace(strings.TrimSpace(id), true)
	if err != nil {
		return MarketplaceSummary{}, err
	}
	staged, next, err := m.stageMarketplace(ctx, MarketplaceRegistrationInput{
		Source: current.Source, Ref: current.Ref, SparsePaths: current.SparsePaths,
	})
	if err != nil {
		return MarketplaceSummary{}, err
	}
	defer os.RemoveAll(staged)
	if next.ID != current.ID {
		return MarketplaceSummary{}, errors.New("marketplace name changed during refresh")
	}
	target := filepath.Join(m.marketRoot, current.ID)
	if err := replaceDirectory(staged, target); err != nil {
		return MarketplaceSummary{}, err
	}
	next.Enabled = current.Enabled
	next.RootPath = target
	next.CatalogPath = marketplaceCatalogPath(target, next.Format)
	m.mu.Lock()
	m.markets[next.ID] = next
	err = m.saveMarketplaceStateLocked()
	m.mu.Unlock()
	if err != nil {
		return MarketplaceSummary{}, err
	}
	return marketplaceSummary(next), nil
}

func (m *Manager) SetMarketplaceEnabled(id string, enabled bool) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	current, err := m.marketplace(strings.TrimSpace(id), true)
	if err != nil {
		return err
	}
	current.Enabled = enabled
	m.mu.Lock()
	m.markets[current.ID] = current
	err = m.saveMarketplaceStateLocked()
	m.mu.Unlock()
	return err
}

func (m *Manager) RemoveMarketplace(id string) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	current, err := m.marketplace(strings.TrimSpace(id), true)
	if err != nil {
		return err
	}
	if !resolvedInside(m.marketRoot, current.RootPath) {
		return errors.New("marketplace cache resolves outside its root")
	}
	if err := os.RemoveAll(current.RootPath); err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.markets, current.ID)
	err = m.saveMarketplaceStateLocked()
	m.mu.Unlock()
	return err
}

func (m *Manager) BrowseMarketplace(ctx context.Context, id string) (MarketplaceCatalog, error) {
	definition, err := m.marketplace(strings.TrimSpace(id), false)
	if err != nil {
		return MarketplaceCatalog{}, err
	}
	catalog, _, err := m.loadMarketplace(ctx, definition)
	if err != nil {
		return MarketplaceCatalog{}, err
	}
	installed, err := m.List()
	if err != nil {
		return MarketplaceCatalog{}, err
	}
	byName := make(map[string]Plugin, len(installed))
	for _, item := range installed {
		byName[item.Name] = item
	}
	for index := range catalog.Plugins {
		if item, exists := byName[catalog.Plugins[index].Name]; exists {
			catalog.Plugins[index].Installed = true
			catalog.Plugins[index].Enabled = item.Enabled
			catalog.Plugins[index].InstalledVersion = item.Version
		}
	}
	return catalog, nil
}

func (m *Manager) InstallMarketplace(
	ctx context.Context,
	marketplaceID, pluginName string,
	replace bool,
) (Plugin, error) {
	definition, err := m.marketplace(strings.TrimSpace(marketplaceID), false)
	if err != nil {
		return Plugin{}, err
	}
	_, sources, err := m.loadMarketplace(ctx, definition)
	if err != nil {
		return Plugin{}, err
	}
	pluginName = strings.TrimSpace(pluginName)
	source, ok := sources[pluginName]
	if !ok {
		return Plugin{}, fmt.Errorf("%w: %s", ErrMarketplacePluginNotFound, pluginName)
	}

	m.opMu.Lock()
	defer m.opMu.Unlock()
	if err := os.MkdirAll(m.root, 0o700); err != nil {
		return Plugin{}, err
	}
	packageRoot, cleanup, err := m.prepareMarketplacePackage(ctx, source, ".marketplace-*")
	if err != nil {
		return Plugin{}, err
	}
	defer cleanup()
	label := "marketplace:" + definition.ID + ":" + pluginName
	return m.installDirectory(packageRoot, label, pluginName, replace)
}

func (m *Manager) PreviewMarketplacePlugin(
	ctx context.Context,
	marketplaceID, pluginName string,
) (MarketplacePluginPreview, error) {
	definition, err := m.marketplace(strings.TrimSpace(marketplaceID), false)
	if err != nil {
		return MarketplacePluginPreview{}, err
	}
	_, sources, err := m.loadMarketplace(ctx, definition)
	if err != nil {
		return MarketplacePluginPreview{}, err
	}
	pluginName = strings.TrimSpace(pluginName)
	source, ok := sources[pluginName]
	if !ok {
		return MarketplacePluginPreview{}, fmt.Errorf(
			"%w: %s",
			ErrMarketplacePluginNotFound,
			pluginName,
		)
	}
	packageRoot, cleanup, err := m.prepareMarketplacePackage(ctx, source, ".preview-*")
	if err != nil {
		return MarketplacePluginPreview{}, err
	}
	defer cleanup()

	manifest, manifestDiagnostics := loadManifest(packageRoot)
	skillCount, skillDiagnostics := inspectSkills(packageRoot)
	servers, mcpDiagnostics := inspectMCP(
		packageRoot,
		filepath.Join(m.dataRoot, pluginName),
		pluginName,
	)
	diagnostics := append(manifestDiagnostics, skillDiagnostics...)
	diagnostics = append(diagnostics, mcpDiagnostics...)
	unsupported := inspectUnsupportedComponents(packageRoot)
	manifestValid := !hasErrors(manifestDiagnostics) && manifest.Name == pluginName
	compatibility := "compatible"
	if !manifestValid || skillCount+len(servers) == 0 {
		compatibility = "unsupported"
	} else if len(unsupported) > 0 || hasErrors(skillDiagnostics) || hasErrors(mcpDiagnostics) {
		compatibility = "partial"
	}
	return MarketplacePluginPreview{
		Name:                  pluginName,
		Valid:                 manifestValid,
		Compatibility:         compatibility,
		SkillCount:            skillCount,
		MCPServerCount:        len(servers),
		UnsupportedComponents: unsupported,
		Diagnostics:           diagnostics,
	}, nil
}

func (m *Manager) marketplace(id string, includeDisabled bool) (marketplaceDefinition, error) {
	m.mu.RLock()
	item, ok := m.markets[id]
	m.mu.RUnlock()
	if !ok {
		return marketplaceDefinition{}, ErrMarketplaceNotFound
	}
	if !includeDisabled && !item.Enabled {
		return marketplaceDefinition{}, ErrMarketplaceDisabled
	}
	return item, nil
}
