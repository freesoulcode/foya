// Package kernel is the composition root for services exposed by the server.
package kernel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/freesoulcode/foya/internal/agent"
	"github.com/freesoulcode/foya/internal/artifact"
	"github.com/freesoulcode/foya/internal/automation"

	"github.com/freesoulcode/foya/internal/broker"
	"github.com/freesoulcode/foya/internal/browseruse"
	canvas "github.com/freesoulcode/foya/internal/canvas"
	"github.com/freesoulcode/foya/internal/channel/feishu"
	"github.com/freesoulcode/foya/internal/config"
	"github.com/freesoulcode/foya/internal/contextdata"
	conversation "github.com/freesoulcode/foya/internal/conversation"
	"github.com/freesoulcode/foya/internal/hooks"
	interaction "github.com/freesoulcode/foya/internal/interaction"
	"github.com/freesoulcode/foya/internal/mcpclient"
	"github.com/freesoulcode/foya/internal/memorymaint"
	model "github.com/freesoulcode/foya/internal/model"
	openai "github.com/freesoulcode/foya/internal/model/openai"
	"github.com/freesoulcode/foya/internal/plugin"
	"github.com/freesoulcode/foya/internal/project"
	subagent "github.com/freesoulcode/foya/internal/subagent"
	workflow "github.com/freesoulcode/foya/internal/workflow"

	"github.com/freesoulcode/foya/internal/sandbox"

	"github.com/freesoulcode/foya/internal/skill"

	"github.com/freesoulcode/foya/internal/storage"

	foyatelemetry "github.com/freesoulcode/foya/internal/telemetry"
	"github.com/freesoulcode/foya/internal/terminal"
	"github.com/freesoulcode/foya/internal/tool"
	"github.com/freesoulcode/foya/internal/websearch"
)

// App is the kernel composition root.
type App struct {
	cfg          config.Config
	service      *Service
	cancel       context.CancelFunc
	mcp          *mcpclient.Manager
	memory       *memorymaint.Manager
	browser      *browseruse.Controller
	channels     *feishu.Manager
	automations  *automation.Manager
	telemetry    *foyatelemetry.Provider
	database     *storage.Database
	instanceLock *storage.InstanceLock
}

// New assembles a kernel from configuration.
func New(cfg config.Config) (*App, error) {
	instanceLock, err := storage.AcquireInstanceLock(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	keepInstanceLock := false
	defer func() {
		if !keepInstanceLock {
			_ = instanceLock.Close()
		}
	}()
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
	sessions, err := conversation.NewManager(database)
	if err != nil {
		return nil, err
	}
	log := conversation.NewStore(database)
	if err := RecoverFileRewinds(context.Background(), log); err != nil {
		return nil, fmt.Errorf("recover interrupted file rewind: %w", err)
	}
	if err := log.PruneFileCheckpoints(context.Background(), time.Now()); err != nil {
		return nil, fmt.Errorf("prune file checkpoints: %w", err)
	}
	bus := broker.New[conversation.Event]()
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

	// Approval gateway and tool registry.
	gw := interaction.NewGateway(bus, log)
	questions := interaction.NewQuestionGateway(bus, log)
	browserController, err := browseruse.NewController(cfg.DataDir, bus, log)
	if err != nil {
		return nil, err
	}
	executionRunner := sandbox.NewRunner()
	backgroundCommands := tool.NewBackgroundCommandManager(executionRunner)
	backgroundCommands.SetNotifier(func(snapshot tool.BackgroundCommandSnapshot) {
		ev := conversation.Event{
			Kind:    conversation.KindBackgroundCommandUpdated,
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
		ev := conversation.Event{Seq: conversation.Seq(doc.Revision), Kind: conversation.KindCanvasUpdated, Session: doc.ID, Time: time.Now(), Payload: doc}
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
	tools.Register(tool.NewUpdateTasksTool(sessions, func(ctx context.Context, s *conversation.Session) {
		ev := conversation.Event{Kind: conversation.KindSessionUpdated, Session: s.ID, Time: time.Now(), Payload: s}
		seq, _ := log.Append(ctx, ev)
		ev.Seq = seq
		_ = bus.PublishMustDeliver(ctx, "session:"+s.ID, ev)
		record, changed, err := workflows.SyncSpecTaskProgress(s.ID, s.Tasks)
		if err != nil {
			failed := conversation.Event{
				Kind:    conversation.KindError,
				Session: s.ID,
				Time:    time.Now(),
				Payload: "Failed to synchronize Spec task list: " + err.Error(),
			}
			failed.Seq, _ = log.Append(ctx, failed)
			_ = bus.PublishMustDeliver(ctx, "session:"+s.ID, failed)
		} else if changed {
			updated := conversation.Event{
				Kind:    conversation.KindWorkflowUpdated,
				Session: s.ID,
				Time:    time.Now(),
				Payload: record,
			}
			updated.Seq, _ = log.Append(ctx, updated)
			_ = bus.PublishMustDeliver(ctx, "session:"+s.ID, updated)
		}
	}))
	homeDir, _ := os.UserHomeDir()
	agents := subagent.NewDefinitionManager(homeDir, subagent.BuiltinDefinitions())
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

	service := NewService(
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
	service.SetConnections(connections)
	service.SetCapabilityManagers(skills, web, mcpManager)
	service.SetPluginManager(plugins)
	service.SetArtifactStore(artifactStore)
	service.SetCanvasStore(canvasStore)
	service.SetAgentManager(agents)
	service.SetSubAgentManager(subagents)
	service.SetProjectManager(projects)
	service.SetContextStore(contextStore)
	service.SetHooksHomeDir(homeDir)
	service.SetCommandManager(workflow.NewCommandManager(homeDir))
	service.SetWorkflowManager(workflows)
	service.SetQuestionGateway(questions)
	service.SetBrowserController(browserController)
	service.SetBackgroundCommandManager(backgroundCommands)
	engine.SetWorkflowCompletionHandler(service.CompleteWorkflow)
	appCtx, cancel := context.WithCancel(context.Background())
	feishuManager, err := feishu.NewManager(
		appCtx,
		cfg.DataDir,
		service,
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
		service,
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
		service.MemoryCompleter,
	)
	if err != nil {
		automationManager.Close()
		feishuManager.Close()
		cancel()
		return nil, err
	}
	service.SetMemoryMaintenanceWake(memoryManager.Wake)
	memoryManager.Start(appCtx)
	go mcpManager.Start(appCtx)
	app := &App{
		cfg: cfg, service: service, cancel: cancel, mcp: mcpManager,
		memory: memoryManager, browser: browserController,
		channels: feishuManager, automations: automationManager,
		telemetry: telemetryProvider,
		database:  database, instanceLock: instanceLock,
	}
	keepDatabase = true
	keepTelemetry = true
	keepInstanceLock = true
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
		Name:     "Imported connection",
		Kind:     legacy.Kind,
		AuthKind: "api_key",
		BaseURL:  legacy.BaseURL,
		APIKey:   legacy.APIKey,
	}}
}

// buildProvider creates an OpenAI-compatible provider and its default model.
func buildProvider(pc config.Provider) (model.Provider, string) {
	p := openai.New(openai.Config{
		BaseURL:       pc.BaseURL,
		APIKey:        pc.APIKey,
		Model:         pc.Model,
		ContextWindow: pc.ContextWindow,
	})
	return p, pc.Model
}

// Service exposes the transport-neutral application layer.
func (a *App) Service() *Service { return a.service }

// Config returns kernel configuration.
func (a *App) Config() config.Config { return a.cfg }

func (a *App) Channels() *feishu.Manager { return a.channels }

func (a *App) Automations() *automation.Manager { return a.automations }

// Close stops background services and releases MCP sessions and processes.
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
	_ = a.instanceLock.Close()
}
