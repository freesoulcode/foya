// Package kernel 是内核的组合根:装配所有 service(session、agent、
// broker、state、provider 等)并暴露给 server。
package kernel

import (
	"context"
	"os"

	"github.com/freesoulcode/foya/internal/agent"
	"github.com/freesoulcode/foya/internal/approval"
	"github.com/freesoulcode/foya/internal/backend"
	"github.com/freesoulcode/foya/internal/broker"
	"github.com/freesoulcode/foya/internal/config"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/mcpclient"
	"github.com/freesoulcode/foya/internal/project"
	"github.com/freesoulcode/foya/internal/provider"
	"github.com/freesoulcode/foya/internal/provider/openai"
	"github.com/freesoulcode/foya/internal/session"
	"github.com/freesoulcode/foya/internal/skill"
	"github.com/freesoulcode/foya/internal/state"
	"github.com/freesoulcode/foya/internal/terminal"
	"github.com/freesoulcode/foya/internal/tool"
	"github.com/freesoulcode/foya/internal/websearch"
)

// App 是内核组合根。
type App struct {
	cfg     config.Config
	backend *backend.Backend
	cancel  context.CancelFunc
	mcp     *mcpclient.Manager
}

// New 按配置装配内核。
func New(cfg config.Config) (*App, error) {
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
	homeDir, _ := os.UserHomeDir()
	skills, err := skill.NewManager(cfg.DataDir, homeDir, nil)
	if err != nil {
		return nil, err
	}
	web, err := websearch.NewManager(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	tools.Register(tool.NewSkillSearchTool(skills))
	tools.Register(tool.NewSkillLoadTool(skills))
	tools.Register(tool.NewWebSearchTool(web, gw))
	tools.Register(tool.NewWebFetchTool(gw))
	mcpManager, err := mcpclient.NewManager(cfg.DataDir, homeDir, tools, gw)
	if err != nil {
		return nil, err
	}
	projects, err := project.NewManager(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	for _, mcpTool := range mcpclient.ControlTools(mcpManager) {
		tools.Register(mcpTool)
	}

	prov, model := buildProvider(cfg.Provider)
	engine := agent.NewEngine(log, bus, sessions, prov, model, tools, gw)
	engine.SetProjectResolver(func(projectID string) (string, bool) {
		item, ok := projects.Get(projectID)
		return item.Path, ok
	})

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
	be.SetCapabilityManagers(skills, web, mcpManager)
	be.SetProjectManager(projects)
	appCtx, cancel := context.WithCancel(context.Background())
	go mcpManager.Start(appCtx)
	return &App{cfg: cfg, backend: be, cancel: cancel, mcp: mcpManager}, nil
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

// Close 停止后台能力并释放 MCP 会话及其子进程。
func (a *App) Close() {
	a.cancel()
	a.mcp.Close()
}
