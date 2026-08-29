// Package kernel 是内核的组合根:装配所有 service(session、agent、
// broker、state、provider 等)并暴露给 server。
package kernel

import (
	"github.com/freesoulcode/foya/internal/agent"
	"github.com/freesoulcode/foya/internal/approval"
	"github.com/freesoulcode/foya/internal/backend"
	"github.com/freesoulcode/foya/internal/broker"
	"github.com/freesoulcode/foya/internal/config"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/provider"
	"github.com/freesoulcode/foya/internal/provider/openai"
	"github.com/freesoulcode/foya/internal/session"
	"github.com/freesoulcode/foya/internal/state"
	"github.com/freesoulcode/foya/internal/terminal"
	"github.com/freesoulcode/foya/internal/tool"
)

// App 是内核组合根。
type App struct {
	cfg     config.Config
	backend *backend.Backend
}

// New 按配置装配内核。
func New(cfg config.Config) *App {
	sessions := session.NewMemManager()
	log := state.NewMemLog()
	bus := broker.New[event.Event]()
	terminalManager := terminal.NewManager()

	// 审批网关与工具注册表。
	gw := approval.NewGateway(bus, log)
	tools := tool.NewRegistry()
	tools.Register(tool.NewBashTool(gw))
	tools.Register(tool.NewReadTool(gw))
	tools.Register(tool.NewWriteTool(gw))
	tools.Register(tool.NewEditTool(gw))

	prov, model := buildProvider(cfg.Provider)
	engine := agent.NewEngine(log, bus, sessions, prov, model, tools, gw)

	be := backend.New(
		sessions,
		log,
		bus,
		engine,
		gw,
		terminalManager,
		buildProvider,
		cfg.Provider,
		cfg.DataDir,
	)
	// Connection catalog takes precedence. Existing installations with only
	// provider.json or environment variables are migrated into one default
	// Connection so no configured endpoint is lost.
	connections := loadConnections(cfg)
	be.SetConnections(connections)
	return &App{cfg: cfg, backend: be}
}

func loadConnections(cfg config.Config) []config.Connection {
	if saved, ok, err := config.LoadConnections(cfg.DataDir); err == nil && ok {
		return saved
	}
	legacy := cfg.Provider
	if saved, ok, err := config.LoadProvider(cfg.DataDir); err == nil && ok {
		legacy = saved
	}
	if legacy.BaseURL == "" && legacy.APIKey == "" && legacy.Model == "" {
		return nil
	}
	return []config.Connection{{
		ID:           "default",
		Name:         "已导入连接",
		Kind:         legacy.Kind,
		AuthKind:     "api_key",
		BaseURL:      legacy.BaseURL,
		APIKey:       legacy.APIKey,
		DefaultModel: legacy.Model,
	}}
}

// buildProvider 按配置构造 OpenAI 兼容 provider,返回 provider 与默认模型名。
func buildProvider(pc config.Provider) (provider.Provider, string) {
	p := openai.New(openai.Config{BaseURL: pc.BaseURL, APIKey: pc.APIKey, Model: pc.Model})
	return p, pc.Model
}

// Backend 暴露业务层,供 server 使用。
func (a *App) Backend() *backend.Backend { return a.backend }

// Config 返回内核配置。
func (a *App) Config() config.Config { return a.cfg }
