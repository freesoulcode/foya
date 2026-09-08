package kernel

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/freesoulcode/foya/internal/mcpclient"
	model "github.com/freesoulcode/foya/internal/model"
	"github.com/freesoulcode/foya/internal/plugin"
	"github.com/freesoulcode/foya/internal/websearch"
)

func (b *Service) WebSearchSettings() (websearch.Settings, error) {
	b.mu.RLock()
	manager := b.web
	b.mu.RUnlock()
	if manager == nil {
		return websearch.Settings{}, errors.New("web search is unavailable")
	}
	return manager.Settings(), nil
}

func (b *Service) UpdateWebSearchSettings(settings websearch.Settings) error {
	b.mu.RLock()
	manager := b.web
	b.mu.RUnlock()
	if manager == nil {
		return errors.New("web search is unavailable")
	}
	return manager.Update(settings)
}

func (b *Service) TestWebSearch(ctx context.Context, providerID, query string) ([]model.SearchResult, error) {
	b.mu.RLock()
	manager := b.web
	b.mu.RUnlock()
	if manager == nil {
		return nil, errors.New("web search is unavailable")
	}
	return manager.Test(ctx, providerID, query)
}

func (b *Service) SearchWeb(ctx context.Context, query string) ([]model.SearchResult, string, error) {
	b.mu.RLock()
	manager := b.web
	b.mu.RUnlock()
	if manager == nil {
		return nil, "", errors.New("web search is unavailable")
	}
	return manager.Search(ctx, websearch.Request{Query: query})
}

func (b *Service) MCPConfig() (mcpclient.Config, error) {
	b.mu.RLock()
	manager := b.mcp
	b.mu.RUnlock()
	if manager == nil {
		return mcpclient.Config{}, errors.New("MCP is unavailable")
	}
	return manager.Config(), nil
}

func (b *Service) ReplaceMCPConfig(ctx context.Context, config mcpclient.Config) error {
	b.mu.RLock()
	manager := b.mcp
	b.mu.RUnlock()
	if manager == nil {
		return errors.New("MCP is unavailable")
	}
	return manager.Replace(ctx, config)
}

func (b *Service) MCPStatuses() ([]mcpclient.Status, error) {
	b.mu.RLock()
	manager := b.mcp
	b.mu.RUnlock()
	if manager == nil {
		return nil, errors.New("MCP is unavailable")
	}
	return manager.Statuses(), nil
}

func (b *Service) SearchMCPRegistry(
	ctx context.Context,
	query string,
) ([]mcpclient.RegistryServer, error) {
	return mcpclient.SearchRegistry(ctx, query)
}

func (b *Service) Plugins() ([]plugin.Plugin, error) {
	b.mu.RLock()
	manager := b.plugins
	mcpManager := b.mcp
	b.mu.RUnlock()
	if manager == nil {
		return nil, errors.New("plugins are unavailable")
	}
	items, err := manager.List()
	if err != nil || mcpManager == nil {
		return items, err
	}
	statuses := mcpManager.Statuses()
	byPlugin := make(map[string][]mcpclient.Status)
	for _, status := range statuses {
		if status.PluginID != "" {
			byPlugin[status.PluginID] = append(byPlugin[status.PluginID], status)
		}
	}
	for index := range items {
		for _, status := range byPlugin[items[index].Name] {
			if status.State == "error" {
				items[index].Diagnostics = append(items[index].Diagnostics, plugin.Diagnostic{
					Component: "mcp:" + status.ID,
					Code:      "mcp_runtime_failed",
					Severity:  "warning",
					Message:   status.Error,
				})
			}
		}
	}
	return items, nil
}

func (b *Service) PluginMarketplaces() ([]plugin.MarketplaceSummary, error) {
	b.mu.RLock()
	manager := b.plugins
	b.mu.RUnlock()
	if manager == nil {
		return nil, errors.New("plugins are unavailable")
	}
	return manager.Marketplaces(), nil
}

func (b *Service) AddPluginMarketplace(
	ctx context.Context,
	input plugin.MarketplaceRegistrationInput,
) (plugin.MarketplaceSummary, error) {
	b.mu.RLock()
	manager := b.plugins
	b.mu.RUnlock()
	if manager == nil {
		return plugin.MarketplaceSummary{}, errors.New("plugins are unavailable")
	}
	return manager.AddMarketplace(ctx, input)
}

func (b *Service) RefreshPluginMarketplace(
	ctx context.Context,
	name string,
) (plugin.MarketplaceSummary, error) {
	b.mu.RLock()
	manager := b.plugins
	b.mu.RUnlock()
	if manager == nil {
		return plugin.MarketplaceSummary{}, errors.New("plugins are unavailable")
	}
	return manager.RefreshMarketplace(ctx, name)
}

func (b *Service) SetPluginMarketplaceEnabled(name string, enabled bool) error {
	b.mu.RLock()
	manager := b.plugins
	b.mu.RUnlock()
	if manager == nil {
		return errors.New("plugins are unavailable")
	}
	return manager.SetMarketplaceEnabled(name, enabled)
}

func (b *Service) RemovePluginMarketplace(name string) error {
	b.mu.RLock()
	manager := b.plugins
	b.mu.RUnlock()
	if manager == nil {
		return errors.New("plugins are unavailable")
	}
	return manager.RemoveMarketplace(name)
}

func (b *Service) BrowsePluginMarketplace(
	ctx context.Context,
	name string,
) (plugin.MarketplaceCatalog, error) {
	b.mu.RLock()
	manager := b.plugins
	b.mu.RUnlock()
	if manager == nil {
		return plugin.MarketplaceCatalog{}, errors.New("plugins are unavailable")
	}
	return manager.BrowseMarketplace(ctx, name)
}

func (b *Service) PreviewMarketplacePlugin(
	ctx context.Context,
	marketplace, name string,
) (plugin.MarketplacePluginPreview, error) {
	b.mu.RLock()
	manager := b.plugins
	b.mu.RUnlock()
	if manager == nil {
		return plugin.MarketplacePluginPreview{}, errors.New("plugins are unavailable")
	}
	return manager.PreviewMarketplacePlugin(ctx, marketplace, name)
}

func (b *Service) InstallPlugin(ctx context.Context, source string, replace bool) (plugin.Plugin, error) {
	b.mu.RLock()
	manager := b.plugins
	b.mu.RUnlock()
	if manager == nil {
		return plugin.Plugin{}, errors.New("plugins are unavailable")
	}
	item, err := manager.Install(ctx, source, replace)
	if err != nil {
		return plugin.Plugin{}, err
	}
	if err := b.syncPluginMCP(); err != nil {
		return item, err
	}
	return item, nil
}

func (b *Service) InstallMarketplacePlugin(
	ctx context.Context,
	marketplace, name string,
	replace bool,
) (plugin.Plugin, error) {
	b.mu.RLock()
	manager := b.plugins
	b.mu.RUnlock()
	if manager == nil {
		return plugin.Plugin{}, errors.New("plugins are unavailable")
	}
	item, err := manager.InstallMarketplace(ctx, marketplace, name, replace)
	if err != nil {
		return plugin.Plugin{}, err
	}
	if err := b.syncPluginMCP(); err != nil {
		return item, err
	}
	return item, nil
}

func (b *Service) SetPluginEnabled(name string, enabled bool) error {
	b.mu.RLock()
	manager := b.plugins
	b.mu.RUnlock()
	if manager == nil {
		return errors.New("plugins are unavailable")
	}
	if err := manager.SetEnabled(name, enabled); err != nil {
		return err
	}
	return b.syncPluginMCP()
}

func (b *Service) RemovePlugin(name string) error {
	b.mu.RLock()
	manager := b.plugins
	b.mu.RUnlock()
	if manager == nil {
		return errors.New("plugins are unavailable")
	}
	if err := manager.Remove(name); err != nil {
		return err
	}
	return b.syncPluginMCP()
}

func (b *Service) syncPluginMCP() error {
	b.mu.RLock()
	plugins := b.plugins
	mcpManager := b.mcp
	b.mu.RUnlock()
	if plugins == nil || mcpManager == nil {
		return errors.New("plugin runtime is unavailable")
	}
	servers, err := plugins.MCPServers()
	if err != nil {
		return err
	}
	return mcpManager.SetPluginServers(servers)
}

func (b *Service) MCPResources(ctx context.Context, serverID string) ([]*mcp.Resource, error) {
	b.mu.RLock()
	manager := b.mcp
	b.mu.RUnlock()
	if manager == nil {
		return nil, errors.New("MCP is unavailable")
	}
	return manager.Resources(ctx, serverID)
}

func (b *Service) MCPReadResource(ctx context.Context, serverID, uri string) (*mcp.ReadResourceResult, error) {
	b.mu.RLock()
	manager := b.mcp
	b.mu.RUnlock()
	if manager == nil {
		return nil, errors.New("MCP is unavailable")
	}
	return manager.ReadResource(ctx, serverID, uri)
}

func (b *Service) MCPPrompts(ctx context.Context, serverID string) ([]*mcp.Prompt, error) {
	b.mu.RLock()
	manager := b.mcp
	b.mu.RUnlock()
	if manager == nil {
		return nil, errors.New("MCP is unavailable")
	}
	return manager.Prompts(ctx, serverID)
}

func (b *Service) MCPGetPrompt(ctx context.Context, serverID, name string, args map[string]string) (*mcp.GetPromptResult, error) {
	b.mu.RLock()
	manager := b.mcp
	b.mu.RUnlock()
	if manager == nil {
		return nil, errors.New("MCP is unavailable")
	}
	return manager.GetPrompt(ctx, serverID, name, args)
}
