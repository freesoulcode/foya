// Package config 管理内核配置。
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
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

	Provider Provider // 模型 provider 配置(BYOK)
}

// Provider 是模型 provider 配置(BYOK:用户自带 base_url + key + model)。
type Provider struct {
	Kind    string `json:"kind"`
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
	Model   string `json:"model"`
}

// Default 返回本地桌面场景的默认配置。
func Default() Config {
	return Config{
		Transport:  TransportUnixSocket,
		Lifecycle:  LifecycleEphemeral,
		SocketPath: DefaultSocketPath(),
		DataDir:    DefaultDataDir(),
		Provider:   providerFromEnv(),
	}
}

// providerFromEnv 从环境变量装配 provider 配置。
func providerFromEnv() Provider {
	return Provider{
		Kind:    "openai",
		BaseURL: os.Getenv("FOYA_PROVIDER_BASE_URL"),
		APIKey:  os.Getenv("FOYA_PROVIDER_API_KEY"),
		Model:   os.Getenv("FOYA_PROVIDER_MODEL"),
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
