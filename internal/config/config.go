// Package config 管理内核配置。
package config

import (
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
	Kind    string // 始终为 "openai"
	BaseURL string
	APIKey  string
	Model   string
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
