// Package config manages kernel configuration.
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

// Transport identifies the kernel transport.
type Transport string

const (
	TransportUnixSocket Transport = "unix"    // Local Unix domain socket.
	TransportTCP        Transport = "tcp_tls" // Remote TCP with TLS.
)

// Lifecycle controls the kernel process lifecycle.
type Lifecycle string

const (
	LifecycleEphemeral Lifecycle = "ephemeral" // Desktop default; starts on demand.
	LifecycleService   Lifecycle = "service"   // Persistent multi-device service.
)

// Config contains kernel runtime settings.
type Config struct {
	Transport  Transport
	Lifecycle  Lifecycle
	SocketPath string // Unix socket path.
	Addr       string // TCP listen address.
	DataDir    string // Event log and SQLite index directory.
	Agents     AgentLimits
	Telemetry  Telemetry
	// DisableExternalIntegrations prevents short-lived CLI commands from
	// starting persisted long-running transports such as the Feishu bot.
	DisableExternalIntegrations bool

	// Provider is the legacy bootstrap connection loaded from the environment.
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

// Telemetry configures the optional OpenTelemetry exporters. Exporter
// endpoints, protocols, headers, and TLS settings use the standard OTEL_*
// environment variables understood by the Go SDK.
type Telemetry struct {
	Enabled        bool
	TracesEnabled  bool
	MetricsEnabled bool
	CaptureContent bool
	ServiceName    string
	Environment    string
}

var ErrInvalidAgentLimits = errors.New("invalid agent limits")

// Provider contains legacy single-connection BYOK settings.
type Provider struct {
	Kind          string `json:"kind"`
	BaseURL       string `json:"base_url"`
	APIKey        string `json:"api_key"`
	Model         string `json:"model"`
	ContextWindow int64  `json:"context_window,omitempty"`
}

// Connection is an independently configured model account or endpoint.
// AuthKind currently supports api_key and can add oauth_subscription without changing
// the session connection_id and model binding.
type Connection struct {
	ID            string                   `json:"id"`
	Name          string                   `json:"name"`
	Type          string                   `json:"type"`
	VideoProtocol string                   `json:"video_protocol,omitempty"`
	Kind          string                   `json:"kind"`
	AuthKind      string                   `json:"auth_kind"`
	BaseURL       string                   `json:"base_url"`
	APIKey        string                   `json:"api_key,omitempty"`
	ModelSettings map[string]ModelSettings `json:"model_settings,omitempty"`
	Models        []string                 `json:"models,omitempty"`
	ModelsCached  bool                     `json:"models_cached,omitempty"`
	SortOrder     int                      `json:"sort_order"`
	// LegacyDefault is read only to migrate older connection catalogs. The
	// first connection by SortOrder is now the new-session preference.
	LegacyDefault bool `json:"is_default,omitempty"`
}

const (
	ConnectionTypeLanguage = "language"
	ConnectionTypeImage    = "image"
	ConnectionTypeVideo    = "video"

	VideoProtocolSeedance  = "seedance"
	VideoProtocolMiniMaxH3 = "minimax_h3"
)

type ModelRef struct {
	ConnectionID string `json:"connection_id"`
	Model        string `json:"model"`
}

type DefaultModels struct {
	Language ModelRef `json:"language"`
	Fast     ModelRef `json:"fast"`
	Image    ModelRef `json:"image"`
	Video    ModelRef `json:"video"`
}

type ModelSettings struct {
	ContextWindow    int64    `json:"context_window,omitempty"`
	MaxInputTokens   int64    `json:"max_input_tokens,omitempty"`
	MaxOutputTokens  int64    `json:"max_output_tokens,omitempty"`
	ImageInput       bool     `json:"image_input"`
	ImageGeneration  bool     `json:"image_generation"`
	VideoGeneration  bool     `json:"video_generation"`
	AudioGeneration  bool     `json:"audio_generation"`
	ToolCalling      bool     `json:"tool_calling"`
	WebSearch        bool     `json:"web_search"`
	ReasoningEfforts []string `json:"reasoning_efforts,omitempty"`
}

func (s ModelSettings) ImageInputSupported() bool {
	return s.ImageInput
}

// ImageGenerationSupported reports whether this model can be used by the
// image-generation canvas adapter.
func (s ModelSettings) ImageGenerationSupported() bool {
	return s.ImageGeneration
}

// VideoGenerationSupported reports whether this model can be used by the
// video-generation canvas adapter.
func (s ModelSettings) VideoGenerationSupported() bool {
	return s.VideoGeneration
}

func (c Connection) Provider() Provider {
	return Provider{
		Kind:    c.Kind,
		BaseURL: c.BaseURL,
		APIKey:  c.APIKey,
	}
}

// Default returns configuration for the local desktop application.
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
		Telemetry: Telemetry{
			Enabled:        envBool("FOYA_OTEL_ENABLED", false),
			TracesEnabled:  envBool("FOYA_OTEL_TRACES_ENABLED", true),
			MetricsEnabled: envBool("FOYA_OTEL_METRICS_ENABLED", false),
			CaptureContent: envBool("FOYA_OTEL_CAPTURE_CONTENT", false),
			ServiceName:    envString("OTEL_SERVICE_NAME", "foya"),
			Environment:    envString("FOYA_OTEL_ENVIRONMENT", "development"),
		},
		Provider: providerFromEnv(),
	}
}

func envBool(name string, fallback bool) bool {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envString(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envInt(name string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(name))
	if err != nil || value < 0 {
		return fallback
	}
	return value
}

// providerFromEnv builds provider settings from environment variables.
func providerFromEnv() Provider {
	return Provider{
		Kind:          "openai",
		BaseURL:       os.Getenv("FOYA_PROVIDER_BASE_URL"),
		APIKey:        os.Getenv("FOYA_PROVIDER_API_KEY"),
		Model:         os.Getenv("FOYA_PROVIDER_MODEL"),
		ContextWindow: int64(envInt("FOYA_PROVIDER_CONTEXT_WINDOW", 0)),
	}
}

// DefaultDataDir returns the kernel data directory under os.UserConfigDir.
func DefaultDataDir() string {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "foya")
}

// DefaultSocketPath returns the local Unix socket in a private user directory.
func DefaultSocketPath() string {
	return filepath.Join(DefaultDataDir(), "kernel.sock")
}

// providerConfigPath returns the persisted provider configuration path.
func providerConfigPath(dataDir string) string {
	return filepath.Join(dataDir, "provider.json")
}

func connectionsConfigPath(dataDir string) string {
	return filepath.Join(dataDir, "connections.json")
}

func agentLimitsConfigPath(dataDir string) string {
	return filepath.Join(dataDir, "agent-limits.json")
}

func defaultModelsConfigPath(dataDir string) string {
	return filepath.Join(dataDir, "default-models.json")
}

func LoadDefaultModels(dataDir string) (DefaultModels, error) {
	data, err := os.ReadFile(defaultModelsConfigPath(dataDir))
	if errors.Is(err, os.ErrNotExist) {
		return DefaultModels{}, nil
	}
	if err != nil {
		return DefaultModels{}, err
	}
	var defaults DefaultModels
	if err := json.Unmarshal(data, &defaults); err != nil {
		return DefaultModels{}, err
	}
	return defaults, nil
}

func SaveDefaultModels(dataDir string, defaults DefaultModels) error {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(defaults, "", "  ")
	if err != nil {
		return err
	}
	path := defaultModelsConfigPath(dataDir)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
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

// LoadProvider reads persisted provider configuration from dataDir.
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

// SaveProvider persists provider configuration with private filesystem permissions.
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
