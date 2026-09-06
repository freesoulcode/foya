package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
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

func (m *Manager) stageMarketplace(
	ctx context.Context,
	input MarketplaceRegistrationInput,
) (string, marketplaceDefinition, error) {
	input.Source = strings.TrimSpace(input.Source)
	input.Ref = strings.TrimSpace(input.Ref)
	if input.Ref != "" && !validGitRevision(input.Ref) {
		return "", marketplaceDefinition{}, errors.New("invalid marketplace Git ref")
	}
	sparsePaths, err := sanitizeSparsePaths(input.SparsePaths)
	if err != nil {
		return "", marketplaceDefinition{}, err
	}
	kind, location, label, err := normalizeSource(input.Source)
	if err != nil {
		return "", marketplaceDefinition{}, err
	}
	if kind == "local" && pathInside(m.marketRoot, location) {
		return "", marketplaceDefinition{}, errors.New("marketplace source cannot be inside its cache root")
	}
	if err := os.MkdirAll(m.marketRoot, 0o700); err != nil {
		return "", marketplaceDefinition{}, err
	}
	staged, err := os.MkdirTemp(m.marketRoot, ".register-*")
	if err != nil {
		return "", marketplaceDefinition{}, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(staged)
		}
	}()
	resolvedSHA := ""
	switch kind {
	case "local":
		if err := copyTree(location, staged); err != nil {
			return "", marketplaceDefinition{}, err
		}
	case "git":
		if err := cloneMarketplaceRepository(ctx, location, staged, input.Ref, sparsePaths); err != nil {
			return "", marketplaceDefinition{}, err
		}
		resolvedSHA, _ = gitOutput(ctx, "-C", staged, "rev-parse", "HEAD")
	default:
		return "", marketplaceDefinition{}, ErrInvalidSource
	}
	definition, err := discoverMarketplace(staged)
	if err != nil {
		return "", marketplaceDefinition{}, err
	}
	definition.Repository = label
	definition.Source = input.Source
	definition.SourceKind = kind
	definition.Location = location
	definition.Ref = input.Ref
	definition.SparsePaths = sparsePaths
	definition.ResolvedSHA = strings.TrimSpace(resolvedSHA)
	definition.Enabled = true
	cleanup = false
	return staged, definition, nil
}

func discoverMarketplace(root string) (marketplaceDefinition, error) {
	for _, location := range marketplaceManifestLocations {
		candidate := filepath.Join(root, location.Relative)
		info, statErr := os.Lstat(candidate)
		if errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if statErr != nil {
			return marketplaceDefinition{}, statErr
		}
		if !info.Mode().IsRegular() || !resolvedInside(root, candidate) {
			return marketplaceDefinition{}, errors.New("marketplace manifest must be a regular file inside its root")
		}
		data, err := readFileLimit(candidate, maxMarketplaceBytes)
		if err != nil {
			return marketplaceDefinition{}, err
		}
		var document marketplaceDocument
		if err := json.Unmarshal(data, &document); err != nil {
			return marketplaceDefinition{}, fmt.Errorf("decode marketplace: %w", err)
		}
		if !validPluginName(document.Name) || len(document.Plugins) > maxMarketplacePlugins {
			return marketplaceDefinition{}, errors.New("invalid marketplace catalog")
		}
		return marketplaceDefinition{
			ID: document.Name, Name: document.Name, Description: document.Metadata.Description,
			Owner: document.Owner, Format: location.Format, RootPath: root,
			CatalogPath: candidate,
		}, nil
	}
	return marketplaceDefinition{}, errors.New(
		"marketplace.json not found under .agents/plugins, .claude-plugin, or .github/plugin",
	)
}

func (m *Manager) loadMarketplace(
	_ context.Context,
	definition marketplaceDefinition,
) (MarketplaceCatalog, map[string]resolvedMarketplaceSource, error) {
	if !resolvedInside(definition.RootPath, definition.CatalogPath) {
		return MarketplaceCatalog{}, nil, errors.New("marketplace manifest resolves outside its root")
	}
	data, err := readFileLimit(definition.CatalogPath, maxMarketplaceBytes)
	if err != nil {
		return MarketplaceCatalog{}, nil, err
	}
	var document marketplaceDocument
	if err := json.Unmarshal(data, &document); err != nil {
		return MarketplaceCatalog{}, nil, fmt.Errorf("decode marketplace: %w", err)
	}
	if document.Name != definition.ID || len(document.Plugins) > maxMarketplacePlugins {
		return MarketplaceCatalog{}, nil, errors.New("invalid marketplace catalog")
	}

	summary := marketplaceSummary(definition)
	if document.Metadata.Description != "" {
		summary.Description = document.Metadata.Description
	}
	if document.Owner.Name != "" {
		summary.Owner = document.Owner
	}
	catalog := MarketplaceCatalog{MarketplaceSummary: summary}
	sources := make(map[string]resolvedMarketplaceSource, len(document.Plugins))
	seen := make(map[string]bool, len(document.Plugins))
	entryDefinition := definition
	if document.Metadata.PluginRoot != "" {
		pluginRoot, pathErr := cleanMarketplacePath(document.Metadata.PluginRoot)
		if pathErr != nil {
			return MarketplaceCatalog{}, nil, fmt.Errorf("invalid marketplace pluginRoot: %w", pathErr)
		}
		entryDefinition.PluginRoot = pluginRoot
	}
	for _, entry := range document.Plugins {
		item := MarketplacePlugin{
			Name: entry.Name, Description: entry.Description, Version: entry.Version,
			Author: entry.Author, Homepage: entry.Homepage, Repository: entry.Repository,
			License: entry.License, Keywords: entry.Keywords, Category: entry.Category,
			Tags: entry.Tags,
		}
		if !validPluginName(entry.Name) {
			item.Reason = "插件名称无效"
		} else if seen[entry.Name] {
			item.Reason = "市场中存在同名插件"
		} else {
			source, resolveErr := resolveMarketplaceSource(entryDefinition, entry.Source)
			if resolveErr != nil {
				item.Reason = resolveErr.Error()
			} else {
				item.Installable = true
				item.Source = marketplaceSourceLabel(source)
				sources[entry.Name] = source
			}
		}
		seen[entry.Name] = true
		catalog.Plugins = append(catalog.Plugins, item)
	}
	sort.SliceStable(catalog.Plugins, func(i, j int) bool {
		return catalog.Plugins[i].Name < catalog.Plugins[j].Name
	})
	return catalog, sources, nil
}

func resolveMarketplaceSource(
	definition marketplaceDefinition,
	raw json.RawMessage,
) (resolvedMarketplaceSource, error) {
	var relative string
	if json.Unmarshal(raw, &relative) == nil {
		if strings.TrimSpace(relative) == "" {
			return resolvedMarketplaceSource{}, errors.New("插件来源不能为空")
		}
		clean, err := cleanMarketplacePath(relative)
		if err != nil {
			return resolvedMarketplaceSource{}, err
		}
		clean = joinMarketplacePath(definition.PluginRoot, clean)
		local, err := marketplacePackagePath(definition.RootPath, clean)
		if err != nil {
			return resolvedMarketplaceSource{}, err
		}
		return resolvedMarketplaceSource{
			repository: definition.Repository,
			localPath:  local,
			path:       clean,
			revision:   definition.ResolvedSHA,
		}, nil
	}
	var object marketplaceSourceObject
	if err := json.Unmarshal(raw, &object); err != nil {
		return resolvedMarketplaceSource{}, errors.New("不支持的插件来源")
	}
	switch object.Source {
	case "local":
		clean, err := cleanMarketplacePath(object.Path)
		if err != nil {
			return resolvedMarketplaceSource{}, err
		}
		clean = joinMarketplacePath(definition.PluginRoot, clean)
		local, err := marketplacePackagePath(definition.RootPath, clean)
		if err != nil {
			return resolvedMarketplaceSource{}, err
		}
		return resolvedMarketplaceSource{
			repository: definition.Repository,
			localPath:  local,
			path:       clean,
			revision:   definition.ResolvedSHA,
		}, nil
	case "github":
		if !githubRepoPattern.MatchString(object.Repo) {
			return resolvedMarketplaceSource{}, errors.New("GitHub 仓库格式无效")
		}
		return gitMarketplaceSource(
			"https://github.com/"+object.Repo+".git",
			object.Repo,
			object.Path,
			object.Ref,
			object.SHA,
		)
	case "url":
		if err := validateMarketplaceGitURL(object.URL); err != nil {
			return resolvedMarketplaceSource{}, err
		}
		return gitMarketplaceSource(
			object.URL,
			object.URL,
			"",
			object.Ref,
			object.SHA,
		)
	case "git-subdir":
		cloneURL := object.URL
		repository := object.URL
		if githubRepoPattern.MatchString(object.URL) {
			cloneURL = "https://github.com/" + object.URL + ".git"
			repository = object.URL
		} else if err := validateMarketplaceGitURL(object.URL); err != nil {
			return resolvedMarketplaceSource{}, err
		}
		return gitMarketplaceSource(
			cloneURL,
			repository,
			object.Path,
			object.Ref,
			object.SHA,
		)
	case "npm":
		return resolvedMarketplaceSource{}, fmt.Errorf(
			"暂不支持 npm Marketplace 来源 %q",
			object.Package,
		)
	default:
		return resolvedMarketplaceSource{}, errors.New("不支持的插件来源类型")
	}
}

func gitMarketplaceSource(
	cloneURL, repository, subdir, ref, sha string,
) (resolvedMarketplaceSource, error) {
	clean, err := cleanMarketplacePath(subdir)
	if err != nil {
		return resolvedMarketplaceSource{}, err
	}
	revision := strings.TrimSpace(ref)
	if sha != "" {
		if !gitCommitPattern.MatchString(sha) {
			return resolvedMarketplaceSource{}, errors.New("Git commit SHA 无效")
		}
		revision = sha
	} else if revision != "" && !validGitRevision(revision) {
		return resolvedMarketplaceSource{}, errors.New("Git ref 无效")
	}
	return resolvedMarketplaceSource{
		cloneURL: cloneURL, repository: repository, path: clean, revision: revision,
	}, nil
}

func (m *Manager) prepareMarketplacePackage(
	ctx context.Context,
	source resolvedMarketplaceSource,
	pattern string,
) (string, func(), error) {
	if source.localPath != "" {
		return source.localPath, func() {}, nil
	}
	if err := os.MkdirAll(m.root, 0o700); err != nil {
		return "", func() {}, err
	}
	download, err := os.MkdirTemp(m.root, pattern)
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(download) }
	if err := cloneRepositoryAt(ctx, source.cloneURL, download, source.revision); err != nil {
		cleanup()
		return "", func() {}, err
	}
	packageRoot := download
	if source.path != "" {
		packageRoot, err = marketplacePackagePath(download, source.path)
		if err != nil {
			cleanup()
			return "", func() {}, err
		}
	}
	return packageRoot, cleanup, nil
}

func cleanMarketplacePath(value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	clean := pathpkg.Clean(value)
	if clean == "." {
		return "", nil
	}
	if value == "" || pathpkg.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errors.New("插件子目录越界")
	}
	return clean, nil
}

func joinMarketplacePath(root, relative string) string {
	if root == "" {
		return relative
	}
	if relative == "" {
		return root
	}
	return pathpkg.Join(root, relative)
}

func sanitizeSparsePaths(values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	seen := make(map[string]bool)
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		clean, err := cleanMarketplacePath(value)
		if err != nil || clean == "" {
			return nil, errors.New("invalid sparse checkout path")
		}
		if !seen[clean] {
			seen[clean] = true
			out = append(out, clean)
		}
	}
	sort.Strings(out)
	return out, nil
}

func marketplacePackagePath(root, relative string) (string, error) {
	candidate := root
	if relative != "" {
		candidate = filepath.Join(root, filepath.FromSlash(relative))
	}
	if !resolvedInside(root, candidate) {
		return "", errors.New("插件子目录越界")
	}
	info, err := os.Stat(candidate)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("插件来源不是目录")
	}
	return candidate, nil
}

func marketplaceSourceLabel(source resolvedMarketplaceSource) string {
	label := source.repository
	if source.path != "" {
		label += ":" + source.path
	}
	if source.revision != "" {
		label += "#" + source.revision
	}
	return label
}

func marketplaceSummary(item marketplaceDefinition) MarketplaceSummary {
	return MarketplaceSummary{
		ID: item.ID, Name: item.Name, Description: item.Description,
		Repository: item.Repository, Owner: item.Owner, Source: item.Source,
		Ref: item.Ref, SparsePaths: append([]string(nil), item.SparsePaths...),
		ResolvedSHA: item.ResolvedSHA, Format: item.Format, Enabled: item.Enabled,
	}
}

func marketplaceCatalogPath(root, format string) string {
	for _, location := range marketplaceManifestLocations {
		if location.Format == format {
			return filepath.Join(root, location.Relative)
		}
	}
	return ""
}

func (m *Manager) marketplaceStatePath() string {
	return filepath.Join(m.dataDir, "plugin-marketplaces.json")
}

func (m *Manager) loadMarketplaceState() error {
	data, err := os.ReadFile(m.marketplaceStatePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var state persistedMarketplaces
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("decode plugin marketplace state: %w", err)
	}
	for _, item := range state.Items {
		if !validPluginName(item.ID) || marketplaceCatalogPath("", item.Format) == "" {
			continue
		}
		kind, location, label, sourceErr := normalizeSource(item.Source)
		if sourceErr != nil {
			continue
		}
		root := filepath.Join(m.marketRoot, item.ID)
		m.markets[item.ID] = marketplaceDefinition{
			ID: item.ID, Name: item.Name, Description: item.Description,
			Repository: label, Owner: item.Owner, Source: item.Source,
			SourceKind: kind, Location: location, Ref: item.Ref,
			SparsePaths: append([]string(nil), item.SparsePaths...),
			ResolvedSHA: item.ResolvedSHA, Format: item.Format, Enabled: item.Enabled,
			RootPath: root, CatalogPath: marketplaceCatalogPath(root, item.Format),
		}
	}
	return nil
}

func (m *Manager) saveMarketplaceStateLocked() error {
	items := make([]persistedMarketplace, 0, len(m.markets))
	for _, item := range m.markets {
		items = append(items, persistedMarketplace{
			ID: item.ID, Name: item.Name, Description: item.Description,
			Repository: item.Repository, Owner: item.Owner, Source: item.Source,
			Ref: item.Ref, SparsePaths: append([]string(nil), item.SparsePaths...),
			ResolvedSHA: item.ResolvedSHA, Format: item.Format, Enabled: item.Enabled,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	data, err := json.MarshalIndent(persistedMarketplaces{Version: 1, Items: items}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(m.dataDir, 0o700); err != nil {
		return err
	}
	temp := m.marketplaceStatePath() + ".tmp"
	if err := os.WriteFile(temp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temp, m.marketplaceStatePath()); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}

func validGitRevision(value string) bool {
	return gitRevisionPattern.MatchString(value) &&
		!strings.Contains(value, "..") &&
		!strings.Contains(value, "//") &&
		!strings.HasSuffix(value, "/") &&
		!strings.HasSuffix(value, ".")
}

func validateMarketplaceGitURL(value string) error {
	kind, _, _, err := normalizeSource(value)
	if err != nil || kind != "git" {
		return errors.New("Marketplace Git URL 必须是不含凭证的 HTTPS 或 Git SSH URL")
	}
	return nil
}

func inspectUnsupportedComponents(root string) []string {
	found := make(map[string]bool)
	for component, paths := range map[string][]string{
		"agents":     {"agents"},
		"commands":   {"commands"},
		"hooks":      {"hooks", "hooks.json"},
		"lsp":        {"lsp.json", ".lsp.json", "lsp-config"},
		"legacy_mcp": {".mcp.json", filepath.Join(".github", "mcp.json")},
	} {
		for _, relative := range paths {
			if _, err := os.Lstat(filepath.Join(root, relative)); err == nil {
				found[component] = true
				break
			}
		}
	}
	data, err := readFileLimit(filepath.Join(root, "plugin.json"), maxManifestBytes)
	if err == nil {
		var raw map[string]json.RawMessage
		if json.Unmarshal(data, &raw) == nil {
			for field, component := range map[string]string{
				"agents":     "agents",
				"commands":   "commands",
				"hooks":      "hooks",
				"lspServers": "lsp",
				"mcpServers": "legacy_mcp",
				"skills":     "legacy_skills",
				"extensions": "extensions",
			} {
				if value := raw[field]; value != nil && !isJSONNull(value) {
					found[component] = true
				}
			}
		}
	}
	out := make([]string, 0, len(found))
	for component := range found {
		out = append(out, component)
	}
	sort.Strings(out)
	return out
}

func cloneMarketplaceRepository(
	ctx context.Context,
	source, destination, revision string,
	sparsePaths []string,
) error {
	if len(sparsePaths) == 0 {
		return cloneRepositoryAt(ctx, source, destination, revision)
	}
	cloneCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := runGitCommand(
		cloneCtx,
		"clone", "--depth", "1", "--filter=blob:none", "--sparse", "--no-checkout",
		"--", source, destination,
	); err != nil {
		return err
	}
	paths := []string{".agents/plugins", ".claude-plugin", ".github/plugin"}
	paths = append(paths, sparsePaths...)
	args := append([]string{"-C", destination, "sparse-checkout", "set"}, paths...)
	if err := runGitCommand(cloneCtx, args...); err != nil {
		return err
	}
	if revision != "" {
		if err := runGitCommand(
			cloneCtx,
			"-C", destination, "fetch", "--depth", "1", "origin", revision,
		); err != nil {
			return err
		}
		return runGitCommand(
			cloneCtx,
			"-C", destination, "checkout", "--detach", "FETCH_HEAD",
		)
	}
	return runGitCommand(cloneCtx, "-C", destination, "checkout", "--force", "HEAD")
}

func cloneRepositoryAt(ctx context.Context, source, destination, revision string) error {
	if revision == "" {
		return cloneRepository(ctx, source, destination)
	}
	cloneCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	commands := [][]string{
		{"init", destination},
		{"-C", destination, "remote", "add", "origin", source},
		{"-C", destination, "fetch", "--depth", "1", "origin", revision},
		{"-C", destination, "checkout", "--detach", "FETCH_HEAD"},
	}
	for _, args := range commands {
		if err := runGitCommand(cloneCtx, args...); err != nil {
			return err
		}
	}
	return nil
}

func runGitCommand(ctx context.Context, args ...string) error {
	command := exec.CommandContext(ctx, "git", args...)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := command.CombinedOutput()
	if err == nil {
		return nil
	}
	message := strings.TrimSpace(string(output))
	if message == "" {
		message = err.Error()
	}
	return fmt.Errorf("git: %s", message)
}

func gitOutput(ctx context.Context, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", args...)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git: %s", strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}
