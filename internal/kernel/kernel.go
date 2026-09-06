// Package kernel 是内核的组合根:装配所有 service(session、agent、
// broker、state、provider 等)并暴露给 server。
package kernel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/freesoulcode/foya/internal/agent"
	"github.com/freesoulcode/foya/internal/agentdef"
	"github.com/freesoulcode/foya/internal/approval"
	"github.com/freesoulcode/foya/internal/artifact"
	"github.com/freesoulcode/foya/internal/automation"
	"github.com/freesoulcode/foya/internal/backend"
	"github.com/freesoulcode/foya/internal/broker"
	"github.com/freesoulcode/foya/internal/browseruse"
	"github.com/freesoulcode/foya/internal/canvas"
	"github.com/freesoulcode/foya/internal/channel/feishu"
	"github.com/freesoulcode/foya/internal/command"
	"github.com/freesoulcode/foya/internal/config"
	"github.com/freesoulcode/foya/internal/contextdata"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/hooks"
	"github.com/freesoulcode/foya/internal/mcpclient"
	"github.com/freesoulcode/foya/internal/memorymaint"
	"github.com/freesoulcode/foya/internal/plugin"
	"github.com/freesoulcode/foya/internal/project"
	"github.com/freesoulcode/foya/internal/provider"
	"github.com/freesoulcode/foya/internal/provider/openai"
	"github.com/freesoulcode/foya/internal/question"
	"github.com/freesoulcode/foya/internal/sandbox"
	"github.com/freesoulcode/foya/internal/session"
	"github.com/freesoulcode/foya/internal/skill"
	"github.com/freesoulcode/foya/internal/state"
	"github.com/freesoulcode/foya/internal/storage"
	"github.com/freesoulcode/foya/internal/subagent"
	foyatelemetry "github.com/freesoulcode/foya/internal/telemetry"
	"github.com/freesoulcode/foya/internal/terminal"
	"github.com/freesoulcode/foya/internal/tool"
	"github.com/freesoulcode/foya/internal/websearch"
	"github.com/freesoulcode/foya/internal/workflow"
)

// App 是内核组合根。
type App struct {
	cfg         config.Config
	backend     *backend.Backend
	cancel      context.CancelFunc
	mcp         *mcpclient.Manager
	memory      *memorymaint.Manager
	browser     *browseruse.Controller
	channels    *feishu.Manager
	automations *automation.Manager
	telemetry   *foyatelemetry.Provider
	database    *storage.Database
}

// New 按配置装配内核。
func New(cfg config.Config) (*App, error) {
	if saved, ok, err := config.LoadAgentLimits(cfg.DataDir); err != nil {
		return nil, err
	} else if ok {
		cfg.Agents = saved
	}
	telemetryProvider, err := foyatelemetry.New(context.Background(), foyatelemetry.Config{
		Enabled:        cfg.Telemetry.Enabled,
		TracesEnabled:  cfg.Telemetry.TracesEnabled,
		MetricsEnabled: cfg.Telemetry.MetricsEnabled,
		CaptureContent: cfg.Telemetry.CaptureContent,
		ServiceName:    cfg.Telemetry.ServiceName,
		Environment:    cfg.Telemetry.Environment,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize telemetry: %w", err)
	}
	keepTelemetry := false
	defer func() {
		if !keepTelemetry {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = telemetryProvider.Shutdown(shutdownCtx)
		}
	}()
	database, err := storage.Open(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	keepDatabase := false
	defer func() {
		if !keepDatabase {
			_ = database.Close()
		}
	}()
	sessions, err := session.NewManager(database)
	if err != nil {
		return nil, err
	}
	log := state.NewStore(database)
	if err := backend.RecoverFileRewinds(context.Background(), log); err != nil {
		return nil, fmt.Errorf("recover interrupted file rewind: %w", err)
	}
	if err := log.PruneFileCheckpoints(context.Background(), time.Now()); err != nil {
		return nil, fmt.Errorf("prune file checkpoints: %w", err)
	}
	bus := broker.New[event.Event]()
	artifactStore, err := artifact.NewFileStore(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	canvasStore, err := canvas.NewStore(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	terminalManager := terminal.NewManager()
	workflows, err := workflow.NewManager(cfg.DataDir)
	if err != nil {
		return nil, err
	}

	// 审批网关与工具注册表。
	gw := approval.NewGateway(bus, log)
	questions := question.NewGateway(bus, log)
	browserController, err := browseruse.NewController(cfg.DataDir, bus, log)
	if err != nil {
		return nil, err
	}
	executionRunner := sandbox.NewRunner()
	backgroundCommands := tool.NewBackgroundCommandManager(executionRunner)
	backgroundCommands.SetNotifier(func(snapshot tool.BackgroundCommandSnapshot) {
		ev := event.Event{
			Kind:    event.KindBackgroundCommandUpdated,
			Session: snapshot.SessionID,
			Time:    time.Now(),
			Payload: snapshot,
		}
		seq, err := log.Append(context.Background(), ev)
		if err != nil {
			return
		}
		ev.Seq = seq
		_ = bus.PublishMustDeliver(context.Background(), "session:"+snapshot.SessionID, ev)
	})
	tools := tool.NewRegistry()
	for _, canvasTool := range tool.CanvasTools(canvasStore, func(ctx context.Context, doc canvas.Document) {
		ev := event.Event{Seq: event.Seq(doc.Revision), Kind: event.KindCanvasUpdated, Session: doc.ID, Time: time.Now(), Payload: doc}
		_ = bus.PublishMustDeliver(ctx, "canvas:"+doc.ID, ev)
	}) {
		tools.Register(canvasTool)
	}
	tools.Register(tool.NewBashToolWithManager(gw, executionRunner, backgroundCommands))
	tools.Register(tool.NewBashStatusTool(backgroundCommands))
	tools.Register(tool.NewBashCancelTool(backgroundCommands))
	tools.Register(tool.NewReadTool(gw))
	tools.Register(tool.NewWriteTool(gw, executionRunner))
	tools.Register(tool.NewEditTool(gw, executionRunner))
	tools.Register(tool.NewDeleteTool(gw))
	tools.Register(tool.NewAskUserTool(questions))
	tools.Register(tool.NewReadTasksTool(sessions))
	tools.Register(tool.NewHistoryReadToolResult(log))
	tools.Register(workflow.NewSubmitSpecTool(workflows))
	tools.Register(tool.NewUpdateTasksTool(sessions, func(ctx context.Context, s *session.Session) {
		ev := event.Event{Kind: event.KindSessionUpdated, Session: s.ID, Time: time.Now(), Payload: s}
		seq, _ := log.Append(ctx, ev)
		ev.Seq = seq
		_ = bus.PublishMustDeliver(ctx, "session:"+s.ID, ev)
		record, changed, err := workflows.SyncSpecTaskProgress(s.ID, s.Tasks)
		if err != nil {
			failed := event.Event{
				Kind:    event.KindError,
				Session: s.ID,
				Time:    time.Now(),
				Payload: "同步 Spec 任务清单失败: " + err.Error(),
			}
			failed.Seq, _ = log.Append(ctx, failed)
			_ = bus.PublishMustDeliver(ctx, "session:"+s.ID, failed)
		} else if changed {
			updated := event.Event{
				Kind:    event.KindWorkflowUpdated,
				Session: s.ID,
				Time:    time.Now(),
				Payload: record,
			}
			updated.Seq, _ = log.Append(ctx, updated)
			_ = bus.PublishMustDeliver(ctx, "session:"+s.ID, updated)
		}
	}))
	homeDir, _ := os.UserHomeDir()
	agents := agentdef.NewManager(homeDir, agentdef.BuiltinDefinitions())
	plugins, err := plugin.NewManager(cfg.DataDir, homeDir)
	if err != nil {
		return nil, err
	}
	skills, err := skill.NewManager(cfg.DataDir, homeDir, skill.BuiltinDefinitions())
	if err != nil {
		return nil, err
	}
	skills.SetPluginRoots(func() []skill.PluginRoot {
		roots := plugins.SkillRoots()
		out := make([]skill.PluginRoot, 0, len(roots))
		for _, root := range roots {
			out = append(out, skill.PluginRoot{PluginID: root.PluginID, Path: root.Path})
		}
		return out
	})
	web, err := websearch.NewManager(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	skillTools := tool.DefaultSkillAvailableTools()
	skillCapabilities := tool.DefaultSkillCapabilities()
	tools.Register(tool.NewSkillSearchTool(skills, skillTools, skillCapabilities))
	tools.Register(tool.NewSkillLoadTool(skills, skillTools, skillCapabilities))
	tools.Register(tool.NewSkillReadResourceTool(skills, skillTools, skillCapabilities))
	tools.Register(tool.NewToolSearchTool(tools))
	tools.Register(tool.NewWebSearchTool(web, gw))
	tools.Register(tool.NewWebFetchTool(gw))
	for _, browserTool := range tool.BrowserTools(browserController, gw) {
		tools.Register(browserTool)
	}
	mcpManager, err := mcpclient.NewManager(cfg.DataDir, homeDir, tools, gw)
	if err != nil {
		return nil, err
	}
	pluginServers, err := plugins.MCPServers()
	if err != nil {
		return nil, err
	}
	if err := mcpManager.SetPluginServers(pluginServers); err != nil {
		return nil, err
	}
	projects, err := project.NewManager(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	contextStore, err := contextdata.NewStore(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	tools.Register(tool.NewMemoryRememberTool(contextStore))
	tools.Register(tool.NewRuleLoadTool(contextStore))
	for _, mcpTool := range mcpclient.ControlTools(mcpManager) {
		tools.Register(mcpTool)
	}

	prov, model := buildProvider(cfg.Provider)
	engine := agent.NewEngine(log, bus, sessions, prov, model, tools, gw)
	engine.SetHookRuntime(hooks.NewRuntime(homeDir))
	engine.SetSkillManager(skills)
	engine.SetWorkflowPolicyResolver(workflows.Policy)
	resolveProject := func(projectID string) (string, bool) {
		item, ok := projects.Get(projectID)
		return item.Path, ok
	}
	if err := contextStore.ConfigureFiles(
		filepath.Join(homeDir, ".foya", "memory"),
		filepath.Join(homeDir, ".foya", "rules"),
		resolveProject,
		func() map[string]string {
			items := projects.List()
			paths := make(map[string]string, len(items))
			for _, item := range items {
				paths[item.ID] = item.Path
			}
			return paths
		},
	); err != nil {
		return nil, err
	}
	engine.SetProjectResolver(resolveProject)
	engine.SetPersistentContextResolver(func(projectID, activity string) ([]string, []string, []string) {
		ruleItems := contextStore.ActiveRules(projectID, activity)
		availableRuleItems := contextStore.AvailableRules(projectID)
		var memoryItems []contextdata.Memory
		if contextStore.MemoryEnabled() {
			memoryItems = contextStore.EffectiveMemories(projectID)
		}
		rules := make([]string, 0, len(ruleItems))
		for _, item := range ruleItems {
			rules = append(rules, item.Content)
		}
		ruleIndex := make([]string, 0, len(availableRuleItems))
		for _, item := range availableRuleItems {
			ruleIndex = append(ruleIndex, item.Name+": "+item.Description)
		}
		memories := make([]string, 0, len(memoryItems))
		for _, item := range memoryItems {
			memories = append(memories, item.Content)
		}
		return rules, ruleIndex, memories
	})
	subagents := subagent.NewManager(
		agents,
		sessions,
		engine,
		log,
		log,
		bus,
		resolveProject,
		subagent.Limits{
			MaxGlobalConcurrency: cfg.Agents.MaxGlobalConcurrency,
			MaxPerRoot:           cfg.Agents.MaxPerRoot,
			MaxChildrenPerRoot:   config.InternalMaxChildrenPerRoot,
			MaxTreeTokens:        cfg.Agents.MaxTreeTokens,
		},
	)
	if err := subagents.EnablePersistence(cfg.DataDir); err != nil {
		return nil, err
	}
	subagents.SetLifecycleCallbacks(
		func(ctx context.Context, lifecycle subagent.Lifecycle) {
			engine.RunSubagentStart(
				ctx,
				lifecycle.ChildSessionID,
				lifecycle.ParentSessionID,
				lifecycle.RunID,
				lifecycle.AgentID,
				lifecycle.AgentType,
				lifecycle.Task,
			)
		},
		func(ctx context.Context, lifecycle subagent.Lifecycle) (bool, string) {
			return engine.RunSubagentStop(
				ctx,
				lifecycle.ChildSessionID,
				lifecycle.ParentSessionID,
				lifecycle.RunID,
				lifecycle.AgentID,
				lifecycle.AgentType,
				lifecycle.Status,
				lifecycle.Output,
				lifecycle.Error,
			)
		},
	)
	engine.SetUsageObserver(subagents.ObserveUsage)
	tools.Register(subagent.NewSearchTool(agents, resolveProject))
	tools.Register(subagent.NewTool(subagents))
	tools.Register(subagent.NewSpawnTool(subagents))
	tools.Register(subagent.NewWaitTool(subagents))
	tools.Register(subagent.NewReadTool(subagents))
	tools.Register(subagent.NewCancelTool(subagents))
	tools.Register(subagent.NewListTool(subagents))

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
	be.SetPluginManager(plugins)
	be.SetArtifactStore(artifactStore)
	be.SetCanvasStore(canvasStore)
	be.SetAgentManager(agents)
	be.SetSubAgentManager(subagents)
	be.SetProjectManager(projects)
	be.SetContextStore(contextStore)
	be.SetHooksHomeDir(homeDir)
	be.SetCommandManager(command.NewManager(homeDir))
	be.SetWorkflowManager(workflows)
	be.SetQuestionGateway(questions)
	be.SetBrowserController(browserController)
	be.SetBackgroundCommandManager(backgroundCommands)
	engine.SetWorkflowCompletionHandler(be.CompleteWorkflow)
	appCtx, cancel := context.WithCancel(context.Background())
	feishuManager, err := feishu.NewManager(
		appCtx,
		cfg.DataDir,
		be,
		nil,
		!cfg.DisableExternalIntegrations,
	)
	if err != nil {
		cancel()
		return nil, err
	}
	automationManager, err := automation.NewManager(
		appCtx,
		cfg.DataDir,
		be,
		!cfg.DisableExternalIntegrations,
	)
	if err != nil {
		feishuManager.Close()
		cancel()
		return nil, err
	}
	memoryManager, err := memorymaint.New(
		cfg.DataDir,
		sessions,
		log,
		contextStore,
		be.MemoryCompleter,
	)
	if err != nil {
		automationManager.Close()
		feishuManager.Close()
		cancel()
		return nil, err
	}
	be.SetMemoryMaintenanceWake(memoryManager.Wake)
	memoryManager.Start(appCtx)
	go mcpManager.Start(appCtx)
	app := &App{
		cfg: cfg, backend: be, cancel: cancel, mcp: mcpManager,
		memory: memoryManager, browser: browserController,
		channels: feishuManager, automations: automationManager,
		telemetry: telemetryProvider,
		database:  database,
	}
	keepDatabase = true
	keepTelemetry = true
	return app, nil
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
		ID:       "default",
		Name:     "已导入连接",
		Kind:     legacy.Kind,
		AuthKind: "api_key",
		BaseURL:  legacy.BaseURL,
		APIKey:   legacy.APIKey,
	}}
}

// buildProvider 按配置构造 OpenAI 兼容 provider,返回 provider 与默认模型名。
func buildProvider(pc config.Provider) (provider.Provider, string) {
	p := openai.New(openai.Config{
		BaseURL:       pc.BaseURL,
		APIKey:        pc.APIKey,
		Model:         pc.Model,
		ContextWindow: pc.ContextWindow,
	})
	return p, pc.Model
}

// Backend 暴露业务层,供 server 使用。
func (a *App) Backend() *backend.Backend { return a.backend }

// Config 返回内核配置。
func (a *App) Config() config.Config { return a.cfg }

func (a *App) Channels() *feishu.Manager { return a.channels }

func (a *App) Automations() *automation.Manager { return a.automations }

// Close 停止后台能力并释放 MCP 会话及其子进程。
func (a *App) Close() {
	a.automations.Close()
	a.channels.Close()
	a.cancel()
	a.browser.Close()
	a.mcp.Close()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = a.telemetry.Shutdown(shutdownCtx)
	_ = a.database.Close()
}
