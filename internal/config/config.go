// Package config 管理内核配置。
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// Transport 是内核对外的传输方式。
type Transport string

const (
	TransportUnixSocket Transport = "unix"    // 本地:Unix domain socket
	TransportTCP        Transport = "tcp_tls" // 远端自部署:TCP + TLS
)

// Lifecycle 是内核生命周期模式。
type Lifecycle string

const (
	LifecycleEphemeral Lifecycle = "ephemeral" // 桌面默认:按需拉起,空闲自动退
	LifecycleService   Lifecycle = "service"   // 常驻守护:多设备场景
)

// Config 是内核运行配置。
type Config struct {
	Transport  Transport
	Lifecycle  Lifecycle
	SocketPath string // Unix socket 路径(TransportUnixSocket 时)
	Addr       string // 监听地址(TransportTCP 时)
	DataDir    string // 事件日志、SQLite 索引所在目录
	Agents     AgentLimits

	// Provider 是旧的单连接启动配置，仅用于从环境变量迁移初始 Connection。
	Provider Provider
}

const (
	InternalMaxChildrenPerRoot = 64
)

type AgentLimits struct {
	MaxGlobalConcurrency int   `json:"max_global_concurrency"`
	MaxPerRoot           int   `json:"max_per_root"`
	MaxTreeTokens        int64 `json:"max_tree_tokens"`
}

var ErrInvalidAgentLimits = errors.New("invalid agent limits")

// Provider 是旧版单连接启动配置(BYOK:用户自带 base_url + key + model)。
type Provider struct {
	Kind          string `json:"kind"`
	BaseURL       string `json:"base_url"`
	APIKey        string `json:"api_key"`
	Model         string `json:"model"`
	ContextWindow int64  `json:"context_window,omitempty"`
}

// Connection 是一个可独立使用的模型账号或端点。
// 本阶段 AuthKind 固定为 api_key；后续可扩展 oauth_subscription 而不影响
// Session 的 connection_id + model 绑定关系。
type Connection struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	AuthKind      string `json:"auth_kind"`
	BaseURL       string `json:"base_url"`
	APIKey        string `json:"api_key,omitempty"`
	DefaultModel  string `json:"default_model"`
	ContextWindow int64  `json:"context_window,omitempty"`
	SortOrder     int    `json:"sort_order"`
	// LegacyDefault is read only to migrate older connection catalogs. The
	// first connection by SortOrder is now the new-session preference.
	LegacyDefault bool `json:"is_default,omitempty"`
}

func (c Connection) Provider() Provider {
	return Provider{
		Kind:          c.Kind,
		BaseURL:       c.BaseURL,
		APIKey:        c.APIKey,
		Model:         c.DefaultModel,
		ContextWindow: c.ContextWindow,
	}
}

// Default 返回本地桌面场景的默认配置。
func Default() Config {
	return Config{
		Transport:  TransportUnixSocket,
		Lifecycle:  LifecycleEphemeral,
		SocketPath: DefaultSocketPath(),
		DataDir:    DefaultDataDir(),
		Agents: AgentLimits{
			MaxGlobalConcurrency: envInt("FOYA_AGENT_MAX_GLOBAL_CONCURRENCY", 4),
			MaxPerRoot:           envInt("FOYA_AGENT_MAX_PER_ROOT", 4),
			MaxTreeTokens:        int64(envInt("FOYA_AGENT_MAX_TREE_TOKENS", 0)),
		},
		Provider: providerFromEnv(),
	}
}

func envInt(name string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(name))
	if err != nil || value < 0 {
		return fallback
	}
	return value
}

// providerFromEnv 从环境变量装配 provider 配置。
func providerFromEnv() Provider {
	return Provider{
		Kind:          "openai",
		BaseURL:       os.Getenv("FOYA_PROVIDER_BASE_URL"),
		APIKey:        os.Getenv("FOYA_PROVIDER_API_KEY"),
		Model:         os.Getenv("FOYA_PROVIDER_MODEL"),
		ContextWindow: int64(envInt("FOYA_PROVIDER_CONTEXT_WINDOW", 0)),
	}
}

// DefaultDataDir 返回内核数据目录(事件日志、索引)。
// 优先用 os.UserConfigDir 下的 foya 子目录。
func DefaultDataDir() string {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "foya")
}

// DefaultSocketPath 返回本地 Unix socket 路径。
// 放在用户私有目录下,靠 0700 目录 + 0600 socket 做单用户信任边界。
func DefaultSocketPath() string {
	return filepath.Join(DefaultDataDir(), "kernel.sock")
}

// providerConfigPath 返回持久化 provider 配置文件路径。
func providerConfigPath(dataDir string) string {
	return filepath.Join(dataDir, "provider.json")
}

func connectionsConfigPath(dataDir string) string {
	return filepath.Join(dataDir, "connections.json")
}

func agentLimitsConfigPath(dataDir string) string {
	return filepath.Join(dataDir, "agent-limits.json")
}

// LoadAgentLimits reads the persisted scheduler limits. A missing file lets the
// caller retain environment-derived defaults.
func LoadAgentLimits(dataDir string) (AgentLimits, bool, error) {
	data, err := os.ReadFile(agentLimitsConfigPath(dataDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return AgentLimits{}, false, nil
		}
		return AgentLimits{}, false, err
	}
	var limits AgentLimits
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&limits); err != nil {
		return AgentLimits{}, false, err
	}
	if err := ValidateAgentLimits(limits); err != nil {
		return AgentLimits{}, false, err
	}
	return limits, true, nil
}

// SaveAgentLimits atomically persists scheduler settings with user-only
// permissions so manual edits and desktop settings share one source.
func SaveAgentLimits(dataDir string, limits AgentLimits) error {
	if err := ValidateAgentLimits(limits); err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(limits, "", "  ")
	if err != nil {
		return err
	}
	path := agentLimitsConfigPath(dataDir)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func ValidateAgentLimits(limits AgentLimits) error {
	values := []struct {
		name  string
		value int
		max   int
	}{
		{"max_global_concurrency", limits.MaxGlobalConcurrency, 256},
		{"max_per_root", limits.MaxPerRoot, 256},
	}
	for _, item := range values {
		if item.value < 1 || item.value > item.max {
			return fmt.Errorf("%w: %s must be between 1 and %d", ErrInvalidAgentLimits, item.name, item.max)
		}
	}
	if limits.MaxTreeTokens < 0 {
		return fmt.Errorf("%w: max_tree_tokens must be zero or greater", ErrInvalidAgentLimits)
	}
	return nil
}

// LoadConnections reads the Connection catalog. If it is absent, callers may
// migrate the legacy provider.json or environment configuration into one entry.
func LoadConnections(dataDir string) ([]Connection, bool, error) {
	b, err := os.ReadFile(connectionsConfigPath(dataDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	var connections []Connection
	if err := json.Unmarshal(b, &connections); err != nil {
		return nil, false, err
	}
	for i := range connections {
		if connections[i].Kind == "" {
			connections[i].Kind = "openai"
		}
		if connections[i].AuthKind == "" {
			connections[i].AuthKind = "api_key"
		}
	}
	return connections, true, nil
}

// SaveConnections persists the full Connection catalog. Keys remain protected
// by the user-private data directory until the Keychain migration lands.
func SaveConnections(dataDir string, connections []Connection) error {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(connections, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(connectionsConfigPath(dataDir), b, 0o600)
}

// LoadProvider 从 dataDir 读取持久化的 provider 配置。
// 文件不存在时返回 (零值, false, nil),供调用方回退到环境变量。
func LoadProvider(dataDir string) (Provider, bool, error) {
	b, err := os.ReadFile(providerConfigPath(dataDir))
	if err != nil {
		if os.IsNotExist(err) {
			return Provider{}, false, nil
		}
		return Provider{}, false, err
	}
	var p Provider
	if err := json.Unmarshal(b, &p); err != nil {
		return Provider{}, false, err
	}
	if p.Kind == "" {
		p.Kind = "openai"
	}
	return p, true, nil
}

// SaveProvider 把 provider 配置持久化到 dataDir。
// 目录权限 0700、文件权限 0600,构成单用户信任边界(脚手架阶段;
// 后续 API Key 改为存 OS keychain)。
func SaveProvider(dataDir string, p Provider) error {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(providerConfigPath(dataDir), b, 0o600)
}
