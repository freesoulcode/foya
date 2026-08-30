// Package websearch provides a provider-neutral web search router.
package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/freesoulcode/foya/internal/provider"
	xhtml "golang.org/x/net/html"
)

const (
	defaultLimit    = 5
	maxLimit        = 10
	maxResponseSize = 2 * 1024 * 1024
)

type ProviderConfig struct {
	ID             string `json:"id"`
	Kind           string `json:"kind"` // google_cse
	Name           string `json:"name"`
	Enabled        bool   `json:"enabled"`
	APIKey         string `json:"api_key,omitempty"`
	HasAPIKey      bool   `json:"has_api_key,omitempty"`
	SearchEngineID string `json:"search_engine_id,omitempty"`
	Endpoint       string `json:"endpoint,omitempty"`
}

type Settings struct {
	Enabled         bool             `json:"enabled"`
	DefaultProvider string           `json:"default_provider,omitempty"`
	Providers       []ProviderConfig `json:"providers"`
}

type Request struct {
	Query  string
	Limit  int
	Native provider.NativeWebSearcher
	Model  string
}

type SearchProvider interface {
	ID() string
	Search(context.Context, string, int) ([]provider.SearchResult, error)
}

type Manager struct {
	dataDir string
	client  *http.Client
	mu      sync.RWMutex
	config  Settings
}

func NewManager(dataDir string) (*Manager, error) {
	m := &Manager{
		dataDir: dataDir,
		client:  &http.Client{Timeout: 20 * time.Second},
		config:  Settings{Enabled: true},
	}
	if err := m.load(); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manager) Settings() Settings {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return publicSettings(m.config)
}

func (m *Manager) Update(next Settings) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	currentByID := make(map[string]ProviderConfig)
	for _, item := range m.config.Providers {
		currentByID[item.ID] = item
	}
	seen := make(map[string]bool)
	normalized := make([]ProviderConfig, 0, len(next.Providers))
	for index, item := range next.Providers {
		item.ID = strings.TrimSpace(item.ID)
		if item.ID == "" {
			item.ID = fmt.Sprintf("search-%d", index+1)
		}
		if seen[item.ID] {
			return fmt.Errorf("duplicate search provider id %q", item.ID)
		}
		seen[item.ID] = true
		if item.Kind != "google_cse" && item.Kind != "bing" && item.Kind != "baidu" {
			return fmt.Errorf("unsupported search provider kind %q", item.Kind)
		}
		item.Name = strings.TrimSpace(item.Name)
		if item.Name == "" {
			item.Name = map[string]string{
				"google_cse": "Google",
				"bing":       "Bing",
				"baidu":      "百度",
			}[item.Kind]
		}
		if item.APIKey == "" {
			item.APIKey = currentByID[item.ID].APIKey
		}
		item.HasAPIKey = false
		normalized = append(normalized, item)
	}
	next.Providers = normalized
	if next.DefaultProvider != "" && !seen[next.DefaultProvider] {
		return fmt.Errorf("default search provider %q not found", next.DefaultProvider)
	}
	m.config = next
	return m.saveLocked()
}

func (m *Manager) Search(ctx context.Context, req Request) ([]provider.SearchResult, string, error) {
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return nil, "", errors.New("search query is required")
	}
	limit := normalizeLimit(req.Limit)
	m.mu.RLock()
	settings := m.config
	m.mu.RUnlock()
	if !settings.Enabled {
		return nil, "", errors.New("web search is disabled")
	}

	if req.Native != nil {
		results, err := req.Native.SearchWeb(ctx, req.Model, query, limit)
		if err == nil && len(results) > 0 {
			return normalizeResults(results, limit), "model", nil
		}
	}
	if settings.DefaultProvider != "" {
		for _, item := range settings.Providers {
			if item.ID != settings.DefaultProvider || !item.Enabled {
				continue
			}
			searcher, err := m.configuredProvider(item)
			if err != nil {
				return nil, item.ID, err
			}
			results, err := searcher.Search(ctx, query, limit)
			if err == nil {
				return normalizeResults(results, limit), item.ID, nil
			}
			break
		}
	}
	results, err := (&duckDuckGoProvider{client: m.client}).Search(ctx, query, limit)
	return normalizeResults(results, limit), "duckduckgo", err
}

func (m *Manager) Test(ctx context.Context, id, query string) ([]provider.SearchResult, error) {
	if id == "duckduckgo" {
		return (&duckDuckGoProvider{client: m.client}).Search(ctx, query, 3)
	}
	m.mu.RLock()
	var selected ProviderConfig
	for _, item := range m.config.Providers {
		if item.ID == id {
			selected = item
			break
		}
	}
	m.mu.RUnlock()
	if selected.ID == "" {
		return nil, fmt.Errorf("search provider %q not found", id)
	}
	searcher, err := m.configuredProvider(selected)
	if err != nil {
		return nil, err
	}
	return searcher.Search(ctx, query, 3)
}

func (m *Manager) configuredProvider(item ProviderConfig) (SearchProvider, error) {
	switch item.Kind {
	case "google_cse":
		if item.APIKey == "" || item.SearchEngineID == "" {
			return nil, errors.New("Google Custom Search requires api_key and search_engine_id")
		}
		return &googleCSEProvider{client: m.client, config: item}, nil
	case "bing":
		if item.APIKey == "" {
			return nil, errors.New("Bing Web Search requires api_key")
		}
		return &bingProvider{client: m.client, config: item}, nil
	case "baidu":
		return &baiduProvider{client: m.client, config: item}, nil
	default:
		return nil, fmt.Errorf("unsupported search provider kind %q", item.Kind)
	}
}

func (m *Manager) path() string { return filepath.Join(m.dataDir, "web-search.json") }

func (m *Manager) load() error {
	data, err := os.ReadFile(m.path())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &m.config); err != nil {
		return fmt.Errorf("decode web search settings: %w", err)
	}
	return nil
}

func (m *Manager) saveLocked() error {
	if err := os.MkdirAll(m.dataDir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m.config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.path(), data, 0o600)
}

func publicSettings(settings Settings) Settings {
	out := settings
	out.Providers = append([]ProviderConfig{}, settings.Providers...)
	for index := range out.Providers {
		out.Providers[index].HasAPIKey = out.Providers[index].APIKey != ""
		out.Providers[index].APIKey = ""
	}
	return out
}

type googleCSEProvider struct {
	client *http.Client
	config ProviderConfig
}

func (p *googleCSEProvider) ID() string { return p.config.ID }

func (p *googleCSEProvider) Search(ctx context.Context, query string, limit int) ([]provider.SearchResult, error) {
	endpoint, _ := url.Parse("https://customsearch.googleapis.com/customsearch/v1")
	values := endpoint.Query()
	values.Set("key", p.config.APIKey)
	values.Set("cx", p.config.SearchEngineID)
	values.Set("q", query)
	values.Set("num", fmt.Sprint(normalizeLimit(limit)))
	endpoint.RawQuery = values.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Google Custom Search returned HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Items []struct {
			Title   string `json:"title"`
			Link    string `json:"link"`
			Snippet string `json:"snippet"`
		} `json:"items"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseSize)).Decode(&payload); err != nil {
		return nil, err
	}
	results := make([]provider.SearchResult, 0, len(payload.Items))
	for index, item := range payload.Items {
		results = append(results, result(index, item.Title, item.Link, item.Snippet))
	}
	return results, nil
}

type duckDuckGoProvider struct {
	client *http.Client
}

type bingProvider struct {
	client *http.Client
	config ProviderConfig
}

func (p *bingProvider) ID() string { return p.config.ID }

func (p *bingProvider) Search(ctx context.Context, query string, limit int) ([]provider.SearchResult, error) {
	endpoint := strings.TrimSpace(p.config.Endpoint)
	if endpoint == "" {
		endpoint = "https://api.bing.microsoft.com/v7.0/search"
	}
	location, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	values := location.Query()
	values.Set("q", query)
	values.Set("count", fmt.Sprint(normalizeLimit(limit)))
	location.RawQuery = values.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, location.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Ocp-Apim-Subscription-Key", p.config.APIKey)
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Bing Web Search returned HTTP %d", resp.StatusCode)
	}
	var payload struct {
		WebPages struct {
			Value []struct {
				Name    string `json:"name"`
				URL     string `json:"url"`
				Snippet string `json:"snippet"`
			} `json:"value"`
		} `json:"webPages"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseSize)).Decode(&payload); err != nil {
		return nil, err
	}
	results := make([]provider.SearchResult, 0, len(payload.WebPages.Value))
	for index, item := range payload.WebPages.Value {
		results = append(results, result(index, item.Name, item.URL, item.Snippet))
	}
	return results, nil
}

type baiduProvider struct {
	client *http.Client
	config ProviderConfig
}

func (p *baiduProvider) ID() string { return p.config.ID }

func (p *baiduProvider) Search(ctx context.Context, query string, limit int) ([]provider.SearchResult, error) {
	endpoint := "https://www.baidu.com/s?wd=" + url.QueryEscape(query) +
		"&rn=" + fmt.Sprint(normalizeLimit(limit))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", fmt.Sprintf("Mozilla/5.0 Foya/%d", 100+rand.IntN(900)))
	req.Header.Set("Accept", "text/html")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Baidu returned HTTP %d", resp.StatusCode)
	}
	root, err := xhtml.Parse(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return nil, err
	}
	return parseBaidu(root, normalizeLimit(limit)), nil
}

func parseBaidu(root *xhtml.Node, limit int) []provider.SearchResult {
	var results []provider.SearchResult
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if len(results) >= limit {
			return
		}
		if node.Type == xhtml.ElementNode && node.Data == "h3" {
			link := firstElement(node, "a")
			if link != nil {
				href := attribute(link, "href")
				title := nodeText(link)
				if href != "" && title != "" {
					container := node.Parent
					snippet := ""
					if container != nil {
						snippet = nodeTextWithout(container, node)
					}
					results = append(results, result(len(results), title, href, snippet))
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return results
}

func (p *duckDuckGoProvider) ID() string { return "duckduckgo" }

func (p *duckDuckGoProvider) Search(ctx context.Context, query string, limit int) ([]provider.SearchResult, error) {
	endpoint := "https://lite.duckduckgo.com/lite/?q=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", fmt.Sprintf("Mozilla/5.0 Foya/%d", 100+rand.IntN(900)))
	req.Header.Set("Accept", "text/html")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusTooManyRequests {
		return nil, errors.New("DuckDuckGo rate limited the search request")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("DuckDuckGo returned HTTP %d", resp.StatusCode)
	}
	root, err := xhtml.Parse(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return nil, err
	}
	return parseDuckDuckGo(root, normalizeLimit(limit)), nil
}

func parseDuckDuckGo(root *xhtml.Node, limit int) []provider.SearchResult {
	var results []provider.SearchResult
	var current *provider.SearchResult
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if len(results) >= limit {
			return
		}
		if node.Type == xhtml.ElementNode && node.Data == "a" && hasClass(node, "result-link") {
			if current != nil && current.URL != "" {
				current.Rank = len(results) + 1
				results = append(results, *current)
			}
			current = &provider.SearchResult{Title: nodeText(node)}
			for _, attr := range node.Attr {
				if attr.Key == "href" {
					current.URL = cleanDuckDuckGoURL(attr.Val)
					current.Source = sourceHost(current.URL)
				}
			}
		}
		if node.Type == xhtml.ElementNode && node.Data == "td" && hasClass(node, "result-snippet") && current != nil {
			current.Snippet = nodeText(node)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	if current != nil && current.URL != "" && len(results) < limit {
		current.Rank = len(results) + 1
		results = append(results, *current)
	}
	return results
}

func hasClass(node *xhtml.Node, class string) bool {
	for _, attr := range node.Attr {
		if attr.Key == "class" {
			for _, candidate := range strings.Fields(attr.Val) {
				if candidate == class {
					return true
				}
			}
		}
	}
	return false
}

func nodeText(node *xhtml.Node) string {
	var builder strings.Builder
	var walk func(*xhtml.Node)
	walk = func(current *xhtml.Node) {
		if current.Type == xhtml.TextNode {
			builder.WriteString(current.Data)
			builder.WriteByte(' ')
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return strings.Join(strings.Fields(builder.String()), " ")
}

func nodeTextWithout(node, excluded *xhtml.Node) string {
	var builder strings.Builder
	var walk func(*xhtml.Node)
	walk = func(current *xhtml.Node) {
		if current == excluded {
			return
		}
		if current.Type == xhtml.TextNode {
			builder.WriteString(current.Data)
			builder.WriteByte(' ')
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return strings.Join(strings.Fields(builder.String()), " ")
}

func firstElement(node *xhtml.Node, name string) *xhtml.Node {
	if node.Type == xhtml.ElementNode && node.Data == name {
		return node
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if found := firstElement(child, name); found != nil {
			return found
		}
	}
	return nil
}

func attribute(node *xhtml.Node, name string) string {
	for _, item := range node.Attr {
		if item.Key == name {
			return item.Val
		}
	}
	return ""
}

func cleanDuckDuckGoURL(raw string) string {
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if strings.HasSuffix(parsed.Hostname(), "duckduckgo.com") {
		if target := parsed.Query().Get("uddg"); target != "" {
			return target
		}
	}
	return raw
}

func result(index int, title, location, snippet string) provider.SearchResult {
	return provider.SearchResult{
		Title: strings.TrimSpace(title), URL: strings.TrimSpace(location),
		Snippet: strings.TrimSpace(snippet), Source: sourceHost(location), Rank: index + 1,
	}
}

func sourceHost(location string) string {
	parsed, err := url.Parse(location)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

func normalizeLimit(limit int) int {
	if limit <= 0 {
		return defaultLimit
	}
	if limit > maxLimit {
		return maxLimit
	}
	return limit
}

func normalizeResults(results []provider.SearchResult, limit int) []provider.SearchResult {
	filtered := results[:0]
	seen := make(map[string]bool)
	for _, item := range results {
		if len(filtered) >= limit || item.URL == "" || seen[item.URL] {
			continue
		}
		parsed, err := url.Parse(item.URL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			continue
		}
		seen[item.URL] = true
		item.Rank = len(filtered) + 1
		if item.Source == "" {
			item.Source = parsed.Hostname()
		}
		filtered = append(filtered, item)
	}
	sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].Rank < filtered[j].Rank })
	return filtered
}
