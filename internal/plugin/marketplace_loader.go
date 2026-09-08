package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

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
			item.Reason = "Invalid plugin name"
		} else if seen[entry.Name] {
			item.Reason = "Duplicate plugin name in marketplace"
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
			return resolvedMarketplaceSource{}, errors.New("Plugin source is required")
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
		return resolvedMarketplaceSource{}, errors.New("Unsupported plugin source")
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
			return resolvedMarketplaceSource{}, errors.New("Invalid GitHub repository format")
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
			"npm Marketplace source %q is not supported",
			object.Package,
		)
	default:
		return resolvedMarketplaceSource{}, errors.New("Unsupported plugin source type")
	}
}
