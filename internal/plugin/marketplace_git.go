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
	"sort"
	"strings"
	"time"
)

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
			return resolvedMarketplaceSource{}, errors.New("Invalid Git commit SHA")
		}
		revision = sha
	} else if revision != "" && !validGitRevision(revision) {
		return resolvedMarketplaceSource{}, errors.New("Invalid Git ref")
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
		return "", errors.New("Plugin subdirectory escapes its root")
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
		return "", errors.New("Plugin subdirectory escapes its root")
	}
	info, err := os.Stat(candidate)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("Plugin source is not a directory")
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
		return errors.New("Marketplace Git URL must be an HTTPS or Git SSH URL without credentials")
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
