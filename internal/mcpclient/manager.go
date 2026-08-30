// Package mcpclient connects external MCP servers and adapts their capabilities
// to Foya's tool/runtime boundary.
package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/freesoulcode/foya/internal/approval"
	foyatool "github.com/freesoulcode/foya/internal/tool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ServerConfig struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Enabled     bool              `json:"enabled"`
	Transport   string            `json:"transport"` // stdio / streamable_http / sse
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Cwd         string            `json:"cwd,omitempty"`
	URL         string            `json:"url,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	BearerToken string            `json:"bearer_token,omitempty"`
	HasToken    bool              `json:"has_token,omitempty"`
}

type Config struct {
	Version int            `json:"version"`
	Servers []ServerConfig `json:"servers"`
}

type claudeConfig struct {
	MCPServers map[string]claudeServer `json:"mcpServers"`
}

type claudeServer struct {
	Command  string            `json:"command,omitempty"`
	Args     []string          `json:"args,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
	Cwd      string            `json:"cwd,omitempty"`
	Type     string            `json:"type,omitempty"`
	URL      string            `json:"url,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	Disabled bool              `json:"disabled,omitempty"`
}

type Status struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	State         string `json:"state"` // disabled / disconnected / connecting / connected / error
	Transport     string `json:"transport"`
	ToolCount     int    `json:"tool_count"`
	ResourceCount int    `json:"resource_count"`
	PromptCount   int    `json:"prompt_count"`
	Error         string `json:"error,omitempty"`
}

type session struct {
	config    ServerConfig
	ctx       context.Context
	client    *mcp.ClientSession
	cancel    context.CancelFunc
	toolNames []string
	status    Status
}

type Manager struct {
	dataDir  string
	homeDir  string
	registry foyatool.Registry
	gateway  approval.Gateway
	mu       sync.RWMutex
	config   Config
	secrets  map[string]string
	sessions map[string]*session
}

func NewManager(dataDir, homeDir string, registry foyatool.Registry, gateway approval.Gateway) (*Manager, error) {
	manager := &Manager{
		dataDir: dataDir, homeDir: homeDir, registry: registry, gateway: gateway,
		config: Config{Version: 1}, secrets: make(map[string]string),
		sessions: make(map[string]*session),
	}
	if err := manager.load(); err != nil {
		return nil, err
	}
	return manager, nil
}

func (m *Manager) Start(ctx context.Context) {
	m.mu.RLock()
	servers := append([]ServerConfig(nil), m.config.Servers...)
	m.mu.RUnlock()
	for _, server := range servers {
		if server.Enabled {
			_ = m.Connect(ctx, server.ID)
		}
	}
}

func (m *Manager) Config() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return publicConfig(m.config)
}

func (m *Manager) Replace(ctx context.Context, next Config) error {
	next.Version = 1
	seen := make(map[string]bool)
	m.mu.RLock()
	current := m.config
	currentSecrets := make(map[string]string, len(m.secrets))
	for id, secret := range m.secrets {
		currentSecrets[id] = secret
	}
	m.mu.RUnlock()
	currentByID := make(map[string]ServerConfig)
	for _, item := range current.Servers {
		currentByID[item.ID] = item
	}
	nextSecrets := make(map[string]string)
	for index := range next.Servers {
		item := &next.Servers[index]
		item.ID = strings.TrimSpace(item.ID)
		if item.ID == "" || seen[item.ID] {
			return fmt.Errorf("invalid or duplicate MCP server id %q", item.ID)
		}
		seen[item.ID] = true
		if item.BearerToken == "" {
			item.BearerToken = currentSecrets[item.ID]
			if item.BearerToken == "" {
				item.BearerToken = currentByID[item.ID].BearerToken
			}
		}
		if item.BearerToken != "" {
			nextSecrets[item.ID] = item.BearerToken
		}
		item.HasToken = false
		if err := validateConfig(*item); err != nil {
			return fmt.Errorf("%s: %w", item.ID, err)
		}
	}
	for id := range currentByID {
		if !seen[id] {
			m.Disconnect(id)
		}
	}
	m.mu.Lock()
	previousConfig := m.config
	previousSecrets := m.secrets
	m.config = next
	m.secrets = nextSecrets
	err := m.saveLocked()
	if err != nil {
		m.config = previousConfig
		m.secrets = previousSecrets
	}
	m.mu.Unlock()
	if err != nil {
		return err
	}
	for _, item := range next.Servers {
		m.Disconnect(item.ID)
		if item.Enabled {
			_ = m.Connect(ctx, item.ID)
		}
	}
	return nil
}

func (m *Manager) Statuses() []Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Status, 0, len(m.config.Servers))
	for _, config := range m.config.Servers {
		if active := m.sessions[config.ID]; active != nil {
			out = append(out, active.status)
			continue
		}
		state := "disconnected"
		if !config.Enabled {
			state = "disabled"
		}
		out = append(out, Status{
			ID: config.ID, Name: config.Name, State: state, Transport: config.Transport,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (m *Manager) Connect(parent context.Context, id string) error {
	config, ok := m.serverConfig(id)
	if !ok {
		return fmt.Errorf("MCP server %q not found", id)
	}
	if !config.Enabled {
		return errors.New("MCP server is disabled")
	}
	m.Disconnect(id)
	ctx, cancel := context.WithCancel(parent)
	active := &session{
		config: config, ctx: ctx, cancel: cancel,
		status: Status{ID: id, Name: config.Name, State: "connecting", Transport: config.Transport},
	}
	m.mu.Lock()
	m.sessions[id] = active
	m.mu.Unlock()

	transport, err := buildTransport(ctx, config)
	if err != nil {
		m.fail(active, err)
		return err
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "foya", Version: "0.1.0"}, &mcp.ClientOptions{
		ToolListChangedHandler: func(_ context.Context, _ *mcp.ToolListChangedRequest) {
			go func() {
				refreshCtx, refreshCancel := context.WithTimeout(active.ctx, 30*time.Second)
				defer refreshCancel()
				_ = m.refreshTools(refreshCtx, active)
			}()
		},
		ResourceListChangedHandler: func(_ context.Context, _ *mcp.ResourceListChangedRequest) {
			go m.refreshCounts(active.ctx, active)
		},
		PromptListChangedHandler: func(_ context.Context, _ *mcp.PromptListChangedRequest) {
			go m.refreshCounts(active.ctx, active)
		},
	})
	connectCtx, stop := context.WithTimeout(ctx, 60*time.Second)
	defer stop()
	clientSession, err := client.Connect(connectCtx, transport, nil)
	if err != nil {
		cancel()
		m.fail(active, err)
		return err
	}
	m.mu.Lock()
	if m.sessions[id] != active {
		m.mu.Unlock()
		_ = clientSession.Close()
		cancel()
		return errors.New("MCP connection was replaced")
	}
	active.client = clientSession
	m.mu.Unlock()
	tools, err := listTools(ctx, clientSession)
	if err != nil {
		_ = clientSession.Close()
		cancel()
		m.fail(active, err)
		return err
	}
	resources, _ := collectResources(ctx, clientSession)
	prompts, _ := collectPrompts(ctx, clientSession)
	if !m.installTools(active, tools) {
		_ = clientSession.Close()
		cancel()
		return errors.New("MCP connection was replaced")
	}
	m.mu.Lock()
	if m.sessions[id] == active {
		active.status = Status{
			ID: config.ID, Name: config.Name, State: "connected", Transport: config.Transport,
			ToolCount: len(tools), ResourceCount: len(resources), PromptCount: len(prompts),
		}
	}
	m.mu.Unlock()
	return nil
}

func (m *Manager) Disconnect(id string) {
	m.mu.Lock()
	active := m.sessions[id]
	delete(m.sessions, id)
	if active == nil {
		m.mu.Unlock()
		return
	}
	for _, name := range active.toolNames {
		m.registry.Unregister(name)
	}
	m.mu.Unlock()
	active.cancel()
	if active.client != nil {
		_ = active.client.Close()
	}
}

func (m *Manager) Close() {
	m.mu.RLock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	m.mu.RUnlock()
	for _, id := range ids {
		m.Disconnect(id)
	}
}

func (m *Manager) Resources(ctx context.Context, id string) ([]*mcp.Resource, error) {
	active, err := m.connected(id)
	if err != nil {
		return nil, err
	}
	return collectResources(ctx, active.client)
}

func (m *Manager) ReadResource(ctx context.Context, id, uri string) (*mcp.ReadResourceResult, error) {
	active, err := m.connected(id)
	if err != nil {
		return nil, err
	}
	return active.client.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
}

func (m *Manager) Prompts(ctx context.Context, id string) ([]*mcp.Prompt, error) {
	active, err := m.connected(id)
	if err != nil {
		return nil, err
	}
	return collectPrompts(ctx, active.client)
}

func (m *Manager) GetPrompt(ctx context.Context, id, name string, args map[string]string) (*mcp.GetPromptResult, error) {
	active, err := m.connected(id)
	if err != nil {
		return nil, err
	}
	return active.client.GetPrompt(ctx, &mcp.GetPromptParams{Name: name, Arguments: args})
}

func (m *Manager) Call(ctx context.Context, id, name string, arguments map[string]any) (*mcp.CallToolResult, error) {
	active, err := m.connected(id)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	return active.client.CallTool(callCtx, &mcp.CallToolParams{Name: name, Arguments: arguments})
}

func (m *Manager) connected(id string) (*session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	active := m.sessions[id]
	if active == nil || active.client == nil || active.status.State != "connected" {
		return nil, fmt.Errorf("MCP server %q is not connected", id)
	}
	return active, nil
}

func (m *Manager) serverConfig(id string) (ServerConfig, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, item := range m.config.Servers {
		if item.ID == id {
			return item, true
		}
	}
	return ServerConfig{}, false
}

func (m *Manager) fail(active *session, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions[active.config.ID] != active {
		return
	}
	active.status.State = "error"
	active.status.Error = err.Error()
}

func (m *Manager) refreshTools(ctx context.Context, active *session) error {
	m.mu.RLock()
	if m.sessions[active.config.ID] != active || active.client == nil {
		m.mu.RUnlock()
		return errors.New("MCP connection is no longer active")
	}
	client := active.client
	m.mu.RUnlock()
	tools, err := listTools(ctx, client)
	if err != nil {
		return err
	}
	if !m.installTools(active, tools) {
		return errors.New("MCP connection is no longer active")
	}
	return nil
}

func (m *Manager) installTools(active *session, tools []*mcp.Tool) bool {
	adapted := make([]foyatool.Tool, 0, len(tools))
	names := make([]string, 0, len(tools))
	for _, remote := range tools {
		item := newMCPTool(m, active.config.ID, remote)
		adapted = append(adapted, item)
		names = append(names, item.Name())
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions[active.config.ID] != active {
		return false
	}
	for _, name := range active.toolNames {
		m.registry.Unregister(name)
	}
	for _, item := range adapted {
		m.registry.RegisterExternal(item)
	}
	active.toolNames = names
	active.status.ToolCount = len(tools)
	return true
}

func (m *Manager) refreshCounts(ctx context.Context, active *session) {
	refreshCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	m.mu.RLock()
	if m.sessions[active.config.ID] != active || active.client == nil {
		m.mu.RUnlock()
		return
	}
	client := active.client
	m.mu.RUnlock()
	resources, resourceErr := collectResources(refreshCtx, client)
	prompts, promptErr := collectPrompts(refreshCtx, client)

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions[active.config.ID] != active {
		return
	}
	if resourceErr == nil {
		active.status.ResourceCount = len(resources)
	}
	if promptErr == nil {
		active.status.PromptCount = len(prompts)
	}
}

func (m *Manager) root() string {
	if m.homeDir != "" {
		return filepath.Join(m.homeDir, ".foya")
	}
	return filepath.Join(m.dataDir, "mcp")
}

func (m *Manager) path() string { return filepath.Join(m.root(), "mcp.json") }

func (m *Manager) credentialsPath() string {
	return filepath.Join(m.root(), "mcp-credentials.json")
}

func (m *Manager) load() error {
	data, err := os.ReadFile(m.path())
	if errors.Is(err, os.ErrNotExist) {
		return m.loadLegacy()
	}
	if err != nil {
		return err
	}
	if err := m.decodeClaudeConfig(data); err != nil {
		return fmt.Errorf("decode MCP config: %w", err)
	}
	return m.loadCredentials()
}

func (m *Manager) saveLocked() error {
	if err := os.MkdirAll(m.root(), 0o700); err != nil {
		return err
	}
	servers := make(map[string]claudeServer, len(m.config.Servers))
	for _, item := range m.config.Servers {
		server := claudeServer{
			Command: item.Command, Args: item.Args, Env: item.Env, Cwd: item.Cwd,
			URL: item.URL, Headers: item.Headers, Disabled: !item.Enabled,
		}
		if item.Transport != "stdio" {
			server.Type = strings.ReplaceAll(item.Transport, "_", "-")
		}
		servers[item.ID] = server
	}
	data, err := json.MarshalIndent(claudeConfig{MCPServers: servers}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(m.path(), data, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(m.path(), 0o600); err != nil {
		return err
	}
	secrets, err := json.MarshalIndent(m.secrets, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(m.credentialsPath(), secrets, 0o600); err != nil {
		return err
	}
	return os.Chmod(m.credentialsPath(), 0o600)
}

func (m *Manager) decodeClaudeConfig(data []byte) error {
	var disk claudeConfig
	if err := json.Unmarshal(data, &disk); err != nil {
		return err
	}
	m.config = Config{Version: 1, Servers: make([]ServerConfig, 0, len(disk.MCPServers))}
	for id, item := range disk.MCPServers {
		transport := "stdio"
		if item.Command == "" {
			transport = strings.ReplaceAll(item.Type, "-", "_")
			if transport == "" || transport == "http" {
				transport = "streamable_http"
			}
		}
		m.config.Servers = append(m.config.Servers, ServerConfig{
			ID: id, Name: id, Enabled: !item.Disabled, Transport: transport,
			Command: item.Command, Args: item.Args, Env: item.Env, Cwd: item.Cwd,
			URL: item.URL, Headers: item.Headers,
		})
	}
	sort.Slice(m.config.Servers, func(i, j int) bool {
		return m.config.Servers[i].ID < m.config.Servers[j].ID
	})
	return nil
}

func (m *Manager) loadCredentials() error {
	data, err := os.ReadFile(m.credentialsPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &m.secrets); err != nil {
		return fmt.Errorf("decode MCP credentials: %w", err)
	}
	for index := range m.config.Servers {
		m.config.Servers[index].BearerToken = m.secrets[m.config.Servers[index].ID]
	}
	return nil
}

func (m *Manager) loadLegacy() error {
	data, err := os.ReadFile(filepath.Join(m.dataDir, "mcp.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &m.config); err != nil {
		return fmt.Errorf("decode legacy MCP config: %w", err)
	}
	for _, item := range m.config.Servers {
		if item.BearerToken != "" {
			m.secrets[item.ID] = item.BearerToken
		}
	}
	if err := m.saveLocked(); err != nil {
		return err
	}
	return os.Remove(filepath.Join(m.dataDir, "mcp.json"))
}

func publicConfig(config Config) Config {
	out := config
	out.Servers = append([]ServerConfig{}, config.Servers...)
	for index := range out.Servers {
		out.Servers[index].HasToken = out.Servers[index].BearerToken != ""
		out.Servers[index].BearerToken = ""
	}
	return out
}

func validateConfig(config ServerConfig) error {
	switch config.Transport {
	case "stdio":
		if strings.TrimSpace(config.Command) == "" {
			return errors.New("stdio transport requires command")
		}
	case "streamable_http", "sse":
		if strings.TrimSpace(config.URL) == "" {
			return errors.New("remote transport requires URL")
		}
	default:
		return fmt.Errorf("unsupported transport %q", config.Transport)
	}
	return nil
}

type headerRoundTripper struct {
	base    http.RoundTripper
	headers map[string]string
	token   string
}

func (r headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	copy := req.Clone(req.Context())
	copy.Header = req.Header.Clone()
	for key, value := range r.headers {
		copy.Header.Set(key, os.ExpandEnv(value))
	}
	if r.token != "" {
		copy.Header.Set("Authorization", "Bearer "+r.token)
	}
	return r.base.RoundTrip(copy)
}

func buildTransport(ctx context.Context, config ServerConfig) (mcp.Transport, error) {
	switch config.Transport {
	case "stdio":
		command := exec.CommandContext(ctx, config.Command, config.Args...)
		command.Dir = config.Cwd
		command.Env = minimalEnvironment(config.Env)
		return &mcp.CommandTransport{Command: command}, nil
	case "streamable_http", "sse":
		client := &http.Client{
			Timeout: 60 * time.Second,
			Transport: headerRoundTripper{
				base: http.DefaultTransport, headers: config.Headers,
				token: config.BearerToken,
			},
		}
		if config.Transport == "sse" {
			return &mcp.SSEClientTransport{Endpoint: config.URL, HTTPClient: client}, nil
		}
		return &mcp.StreamableClientTransport{Endpoint: config.URL, HTTPClient: client}, nil
	default:
		return nil, fmt.Errorf("unsupported MCP transport %q", config.Transport)
	}
}

func minimalEnvironment(explicit map[string]string) []string {
	allowed := []string{"PATH", "HOME", "USER", "SHELL", "LANG", "TMPDIR", "TEMP", "TMP", "SYSTEMROOT"}
	values := make(map[string]string)
	for _, key := range allowed {
		if value := os.Getenv(key); value != "" {
			values[key] = value
		}
	}
	for key, value := range explicit {
		values[key] = os.ExpandEnv(value)
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+values[key])
	}
	return out
}

func listTools(ctx context.Context, client *mcp.ClientSession) ([]*mcp.Tool, error) {
	var out []*mcp.Tool
	for item, err := range client.Tools(ctx, nil) {
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func collectResources(ctx context.Context, client *mcp.ClientSession) ([]*mcp.Resource, error) {
	var out []*mcp.Resource
	for item, err := range client.Resources(ctx, nil) {
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func collectPrompts(ctx context.Context, client *mcp.ClientSession) ([]*mcp.Prompt, error) {
	var out []*mcp.Prompt
	for item, err := range client.Prompts(ctx, nil) {
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}
