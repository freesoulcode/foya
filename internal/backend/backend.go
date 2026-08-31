// Package backend 是传输无关的业务层:管理多连接、多会话、事件扇出、
// 实例级鉴权。它不关心底层是 Unix socket 还是 TCP,server 层把请求
// 转成对 Backend 的调用。
//
// 这是「一个内核多客户端」的落地关键:session 归 Backend 所有,多个
// 客户端连接可订阅同一 session,Backend 负责把事件扇出给所有订阅者。
package backend

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/freesoulcode/foya/internal/agent"
	"github.com/freesoulcode/foya/internal/agentdef"
	"github.com/freesoulcode/foya/internal/approval"
	"github.com/freesoulcode/foya/internal/broker"
	"github.com/freesoulcode/foya/internal/config"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/mcpclient"
	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/project"
	"github.com/freesoulcode/foya/internal/provider"
	"github.com/freesoulcode/foya/internal/session"
	"github.com/freesoulcode/foya/internal/skill"
	"github.com/freesoulcode/foya/internal/state"
	"github.com/freesoulcode/foya/internal/subagent"
	"github.com/freesoulcode/foya/internal/terminal"
	"github.com/freesoulcode/foya/internal/websearch"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	ErrConnectionNotFound = fmt.Errorf("connection not found")
	ErrConnectionInUse    = fmt.Errorf("connection is in use by a session")
	ErrUnsupportedAuth    = fmt.Errorf("unsupported connection auth kind")
	ErrProjectInUse       = fmt.Errorf("project is in use by a session")
)

// ProviderBuilder 按 provider 配置构造 provider 与默认模型名。
type ProviderBuilder func(config.Provider) (provider.Provider, string)

// Backend 是内核业务的统一入口(传输无关)。
type Backend struct {
	sessions  session.Manager
	log       *state.MemLog
	bus       *broker.Broker[event.Event]
	engine    *agent.Engine
	approval  approval.Gateway
	terminal  terminal.Manager
	skills    *skill.Manager
	web       *websearch.Manager
	mcp       *mcpclient.Manager
	projects  *project.Manager
	agents    *agentdef.Manager
	subagents *subagent.Manager

	buildProvider     ProviderBuilder
	dataDir           string
	mu                sync.RWMutex
	projectMu         sync.Mutex
	connections       map[string]config.Connection
	providers         map[string]provider.Provider
	firstConnectionID string
	turns             *turnScheduler
}

// SetCapabilityManagers attaches optional capability services assembled by the
// kernel composition root.
func (b *Backend) SetCapabilityManagers(skills *skill.Manager, web *websearch.Manager, mcp *mcpclient.Manager) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.skills = skills
	b.web = web
	b.mcp = mcp
}

func (b *Backend) SetProjectManager(projects *project.Manager) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.projects = projects
}

func (b *Backend) SetAgentManager(agents *agentdef.Manager) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.agents = agents
}

func (b *Backend) SetSubAgentManager(manager *subagent.Manager) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subagents = manager
}

func (b *Backend) AgentLimits() (config.AgentLimits, error) {
	b.mu.RLock()
	manager := b.subagents
	b.mu.RUnlock()
	if manager == nil {
		return config.AgentLimits{}, errors.New("sub-agents are unavailable")
	}
	limits := manager.Limits()
	return config.AgentLimits{
		MaxGlobalConcurrency: limits.MaxGlobalConcurrency,
		MaxPerRoot:           limits.MaxPerRoot,
		MaxTreeTokens:        limits.MaxTreeTokens,
	}, nil
}

func (b *Backend) UpdateAgentLimits(limits config.AgentLimits) error {
	if err := config.ValidateAgentLimits(limits); err != nil {
		return err
	}
	b.mu.RLock()
	manager := b.subagents
	dataDir := b.dataDir
	b.mu.RUnlock()
	if manager == nil {
		return errors.New("sub-agents are unavailable")
	}
	if err := config.SaveAgentLimits(dataDir, limits); err != nil {
		return err
	}
	manager.UpdateLimits(subagent.Limits{
		MaxGlobalConcurrency: limits.MaxGlobalConcurrency,
		MaxPerRoot:           limits.MaxPerRoot,
		MaxTreeTokens:        limits.MaxTreeTokens,
	})
	return nil
}

func (b *Backend) AgentRuns(sessionID string) ([]subagent.Snapshot, error) {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return nil, session.ErrNotFound
	}
	b.mu.RLock()
	manager := b.subagents
	b.mu.RUnlock()
	if manager == nil {
		return nil, errors.New("sub-agents are unavailable")
	}
	return manager.List(sessionID), nil
}

func (b *Backend) AgentRun(sessionID, runID string) (subagent.Snapshot, error) {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return subagent.Snapshot{}, session.ErrNotFound
	}
	b.mu.RLock()
	manager := b.subagents
	b.mu.RUnlock()
	if manager == nil {
		return subagent.Snapshot{}, errors.New("sub-agents are unavailable")
	}
	item, err := manager.Read(runID)
	if err == nil && item.ParentSessionID != sessionID {
		return subagent.Snapshot{}, os.ErrNotExist
	}
	return item, err
}

func (b *Backend) StartAgent(ctx context.Context, request subagent.SpawnRequest) (subagent.Snapshot, error) {
	b.mu.RLock()
	manager := b.subagents
	b.mu.RUnlock()
	if manager == nil {
		return subagent.Snapshot{}, errors.New("sub-agents are unavailable")
	}
	return manager.Start(ctx, request)
}

func (b *Backend) WaitAgents(
	ctx context.Context,
	sessionID string,
	ids []string,
	waitAll bool,
) ([]subagent.Snapshot, error) {
	b.mu.RLock()
	manager := b.subagents
	b.mu.RUnlock()
	if manager == nil {
		return nil, errors.New("sub-agents are unavailable")
	}
	for _, id := range ids {
		item, err := manager.Read(id)
		if err != nil {
			return nil, err
		}
		if item.ParentSessionID != sessionID {
			return nil, os.ErrNotExist
		}
	}
	return manager.Wait(ctx, ids, waitAll)
}

func (b *Backend) CancelAgent(sessionID, runID string) error {
	if _, err := b.AgentRun(sessionID, runID); err != nil {
		return err
	}
	b.mu.RLock()
	manager := b.subagents
	b.mu.RUnlock()
	return manager.Cancel(runID)
}

func (b *Backend) AgentBudget(sessionID string) (subagent.Budget, error) {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return subagent.Budget{}, session.ErrNotFound
	}
	b.mu.RLock()
	manager := b.subagents
	b.mu.RUnlock()
	if manager == nil {
		return subagent.Budget{}, errors.New("sub-agents are unavailable")
	}
	return manager.Budget(sessionID), nil
}

// Agents returns effective builtin and user-level agent definitions.
func (b *Backend) Agents(ctx context.Context) ([]agentdef.Definition, error) {
	b.mu.RLock()
	manager := b.agents
	b.mu.RUnlock()
	if manager == nil {
		return nil, errors.New("agents are unavailable")
	}
	return manager.List(ctx, "", "")
}

// ProjectAgents returns effective definitions with the project's definitions
// taking precedence over user and builtin scopes.
func (b *Backend) ProjectAgents(ctx context.Context, projectID string) ([]agentdef.Definition, error) {
	b.mu.RLock()
	agentManager := b.agents
	projectManager := b.projects
	b.mu.RUnlock()
	if agentManager == nil || projectManager == nil {
		return nil, errors.New("project agents are unavailable")
	}
	item, ok := projectManager.Get(projectID)
	if !ok {
		return nil, project.ErrNotFound
	}
	return agentManager.List(ctx, item.ID, item.Path)
}

func (b *Backend) Projects() ([]project.Project, error) {
	b.mu.RLock()
	manager := b.projects
	b.mu.RUnlock()
	if manager == nil {
		return nil, errors.New("projects are unavailable")
	}
	return manager.List(), nil
}

func (b *Backend) RegisterProject(path, name string) (project.Project, error) {
	b.projectMu.Lock()
	defer b.projectMu.Unlock()
	b.mu.RLock()
	manager := b.projects
	b.mu.RUnlock()
	if manager == nil {
		return project.Project{}, errors.New("projects are unavailable")
	}
	return manager.Create(path, name)
}

func (b *Backend) Project(id string) (project.Project, error) {
	b.mu.RLock()
	manager := b.projects
	b.mu.RUnlock()
	if manager == nil {
		return project.Project{}, errors.New("projects are unavailable")
	}
	item, ok := manager.Get(id)
	if !ok {
		return project.Project{}, project.ErrNotFound
	}
	return item, nil
}

func (b *Backend) UpdateProject(
	id string,
	name *string,
	pinned *bool,
) (project.Project, error) {
	b.projectMu.Lock()
	defer b.projectMu.Unlock()
	b.mu.RLock()
	manager := b.projects
	b.mu.RUnlock()
	if manager == nil {
		return project.Project{}, errors.New("projects are unavailable")
	}
	return manager.Update(id, name, pinned)
}

func (b *Backend) DeleteProject(id string) error {
	b.projectMu.Lock()
	defer b.projectMu.Unlock()
	for _, item := range b.sessions.List() {
		if item.ProjectID == id {
			return ErrProjectInUse
		}
	}
	b.mu.RLock()
	manager := b.projects
	b.mu.RUnlock()
	if manager == nil {
		return errors.New("projects are unavailable")
	}
	return manager.Delete(id)
}

func (b *Backend) Skills(ctx context.Context) ([]skill.Skill, error) {
	b.mu.RLock()
	manager := b.skills
	b.mu.RUnlock()
	if manager == nil {
		return nil, errors.New("skills are unavailable")
	}
	return manager.List(ctx, "", "")
}

func (b *Backend) AllSkills(ctx context.Context) ([]skill.Skill, error) {
	b.mu.RLock()
	manager := b.skills
	b.mu.RUnlock()
	if manager == nil {
		return nil, errors.New("skills are unavailable")
	}
	return manager.ListAll(ctx, "", "")
}

func (b *Backend) ProjectSkills(ctx context.Context, projectID string) ([]skill.Skill, error) {
	b.mu.RLock()
	skillManager := b.skills
	projectManager := b.projects
	b.mu.RUnlock()
	if skillManager == nil || projectManager == nil {
		return nil, errors.New("project skills are unavailable")
	}
	item, ok := projectManager.Get(projectID)
	if !ok {
		return nil, project.ErrNotFound
	}
	return skillManager.ListAll(ctx, item.ID, item.Path)
}

func (b *Backend) SetSkillEnabled(ref string, enabled bool) error {
	b.mu.RLock()
	manager := b.skills
	b.mu.RUnlock()
	if manager == nil {
		return errors.New("skills are unavailable")
	}
	return manager.SetEnabled(ref, enabled)
}

func (b *Backend) WebSearchSettings() (websearch.Settings, error) {
	b.mu.RLock()
	manager := b.web
	b.mu.RUnlock()
	if manager == nil {
		return websearch.Settings{}, errors.New("web search is unavailable")
	}
	return manager.Settings(), nil
}

func (b *Backend) UpdateWebSearchSettings(settings websearch.Settings) error {
	b.mu.RLock()
	manager := b.web
	b.mu.RUnlock()
	if manager == nil {
		return errors.New("web search is unavailable")
	}
	return manager.Update(settings)
}

func (b *Backend) TestWebSearch(ctx context.Context, providerID, query string) ([]provider.SearchResult, error) {
	b.mu.RLock()
	manager := b.web
	b.mu.RUnlock()
	if manager == nil {
		return nil, errors.New("web search is unavailable")
	}
	return manager.Test(ctx, providerID, query)
}

func (b *Backend) SearchWeb(ctx context.Context, query string) ([]provider.SearchResult, string, error) {
	b.mu.RLock()
	manager := b.web
	b.mu.RUnlock()
	if manager == nil {
		return nil, "", errors.New("web search is unavailable")
	}
	return manager.Search(ctx, websearch.Request{Query: query})
}

func (b *Backend) MCPConfig() (mcpclient.Config, error) {
	b.mu.RLock()
	manager := b.mcp
	b.mu.RUnlock()
	if manager == nil {
		return mcpclient.Config{}, errors.New("MCP is unavailable")
	}
	return manager.Config(), nil
}

func (b *Backend) ReplaceMCPConfig(ctx context.Context, config mcpclient.Config) error {
	b.mu.RLock()
	manager := b.mcp
	b.mu.RUnlock()
	if manager == nil {
		return errors.New("MCP is unavailable")
	}
	return manager.Replace(ctx, config)
}

func (b *Backend) MCPStatuses() ([]mcpclient.Status, error) {
	b.mu.RLock()
	manager := b.mcp
	b.mu.RUnlock()
	if manager == nil {
		return nil, errors.New("MCP is unavailable")
	}
	return manager.Statuses(), nil
}

func (b *Backend) SearchMCPRegistry(
	ctx context.Context,
	query string,
) ([]mcpclient.RegistryServer, error) {
	return mcpclient.SearchRegistry(ctx, query)
}

func (b *Backend) MCPResources(ctx context.Context, serverID string) ([]*mcp.Resource, error) {
	b.mu.RLock()
	manager := b.mcp
	b.mu.RUnlock()
	if manager == nil {
		return nil, errors.New("MCP is unavailable")
	}
	return manager.Resources(ctx, serverID)
}

func (b *Backend) MCPReadResource(ctx context.Context, serverID, uri string) (*mcp.ReadResourceResult, error) {
	b.mu.RLock()
	manager := b.mcp
	b.mu.RUnlock()
	if manager == nil {
		return nil, errors.New("MCP is unavailable")
	}
	return manager.ReadResource(ctx, serverID, uri)
}

func (b *Backend) MCPPrompts(ctx context.Context, serverID string) ([]*mcp.Prompt, error) {
	b.mu.RLock()
	manager := b.mcp
	b.mu.RUnlock()
	if manager == nil {
		return nil, errors.New("MCP is unavailable")
	}
	return manager.Prompts(ctx, serverID)
}

func (b *Backend) MCPGetPrompt(ctx context.Context, serverID, name string, args map[string]string) (*mcp.GetPromptResult, error) {
	b.mu.RLock()
	manager := b.mcp
	b.mu.RUnlock()
	if manager == nil {
		return nil, errors.New("MCP is unavailable")
	}
	return manager.GetPrompt(ctx, serverID, name, args)
}

// New 组装一个 Backend。
func New(
	sessions session.Manager,
	log *state.MemLog,
	bus *broker.Broker[event.Event],
	engine *agent.Engine,
	gw approval.Gateway,
	terminalManager terminal.Manager,
	build ProviderBuilder,
	provCfg config.Provider,
	dataDir string,
) *Backend {
	return &Backend{
		sessions:      sessions,
		log:           log,
		bus:           bus,
		engine:        engine,
		approval:      gw,
		terminal:      terminalManager,
		buildProvider: build,
		dataDir:       dataDir,
		connections:   make(map[string]config.Connection),
		providers:     make(map[string]provider.Provider),
		turns:         newTurnScheduler(),
	}
}

// CreateSession 新建会话。
func (b *Backend) CreateSession(opts session.CreateOptions) (*session.Session, error) {
	b.projectMu.Lock()
	defer b.projectMu.Unlock()
	b.mu.RLock()
	connectionID := opts.ConnectionID
	if connectionID == "" {
		connectionID = b.firstConnectionID
	}
	connection, exists := b.connections[connectionID]
	b.mu.RUnlock()
	if connectionID != "" && !exists {
		return nil, fmt.Errorf("%w: %q", ErrConnectionNotFound, connectionID)
	}
	if opts.Model == "" && exists {
		opts.Model = connection.DefaultModel
	}
	opts.ConnectionID = connectionID
	if err := b.resolveSessionProject(&opts); err != nil {
		return nil, err
	}
	return b.sessions.Create(opts)
}

// UpdateSession 局部更新会话可变字段。
func (b *Backend) UpdateSession(
	ctx context.Context,
	id string,
	connectionID, model, reasoningEffort, projectID, approvalMode *string,
) (*session.Session, error) {
	b.projectMu.Lock()
	defer b.projectMu.Unlock()
	if connectionID != nil {
		b.mu.RLock()
		_, exists := b.connections[*connectionID]
		b.mu.RUnlock()
		if !exists {
			return nil, fmt.Errorf("%w: %q", ErrConnectionNotFound, *connectionID)
		}
	}
	if projectID != nil && *projectID != "" {
		if _, err := b.Project(*projectID); err != nil {
			return nil, err
		}
	}
	updated, err := b.sessions.Update(id, connectionID, model, reasoningEffort, projectID, approvalMode)
	if err != nil {
		return nil, err
	}
	b.broadcastSession(ctx, updated)
	return updated, nil
}

func (b *Backend) resolveSessionProject(opts *session.CreateOptions) error {
	b.mu.RLock()
	manager := b.projects
	b.mu.RUnlock()
	if manager == nil {
		return nil
	}
	if opts.ProjectID != "" {
		_, ok := manager.Get(opts.ProjectID)
		if !ok {
			return project.ErrNotFound
		}
	}
	return nil
}

// RenameSession 手动改名。
func (b *Backend) RenameSession(ctx context.Context, id, title string) (*session.Session, error) {
	if err := b.sessions.Rename(id, title); err != nil {
		return nil, err
	}
	s, ok := b.sessions.Get(id)
	if !ok {
		return nil, session.ErrNotFound
	}
	b.broadcastSession(ctx, s)
	return s, nil
}

// PinSession 置顶/取消置顶会话,变更后广播 session_updated。
func (b *Backend) PinSession(ctx context.Context, id string, pinned bool) (*session.Session, error) {
	s, err := b.sessions.SetPinned(id, pinned)
	if err != nil {
		return nil, err
	}
	b.broadcastSession(ctx, s)
	return s, nil
}

// deleteTurnGrace 是删除会话时等待活跃回合彻底收尾的上限。
const deleteTurnGrace = 3 * time.Second

// DeleteSession 删除会话:
//  1. 先中断正在跑的回合并等其 goroutine 彻底退出,确保清理后不再有事件写入;
//  2. 删除会话元数据与事件日志(日志层标记删除,迟到事件在 Append 处丢弃);
//  3. 广播 session_deleted 通知在线客户端移除。
//
// session_deleted 属于会话目录层事件,只广播、不写入该会话分区日志——
// 会话已不存在,持久化它没有读者,重连时靠 list_sessions 自然收敛。
func (b *Backend) DeleteSession(ctx context.Context, id string) error {
	if _, ok := b.sessions.Get(id); !ok {
		return session.ErrNotFound
	}
	all := b.sessions.List()
	var ordered []string
	var visit func(string)
	visit = func(parentID string) {
		for _, item := range all {
			if item.ParentID == parentID {
				visit(item.ID)
			}
		}
		ordered = append(ordered, parentID)
	}
	visit(id)
	for _, sessionID := range ordered {
		b.stopSessionAndWait(sessionID, deleteTurnGrace)
		b.terminal.CloseSession(sessionID)
		b.approval.ClearSession(sessionID)
		if err := b.sessions.Delete(sessionID); err != nil {
			return err
		}
		b.log.Delete(sessionID)
		ev := event.Event{
			Kind:    event.KindSessionDeleted,
			Session: sessionID,
			Time:    time.Now(),
			Payload: map[string]string{"id": sessionID},
		}
		_ = b.bus.PublishMustDeliver(ctx, "session:"+sessionID, ev)
	}
	return nil
}

// broadcastSession 把会话当前状态作为 session_updated 事件持久化并广播。
func (b *Backend) broadcastSession(ctx context.Context, s *session.Session) {
	ev := event.Event{Kind: event.KindSessionUpdated, Session: s.ID, Time: time.Now(), Payload: s}
	seq, _ := b.log.Append(ctx, ev)
	ev.Seq = seq
	_ = b.bus.PublishMustDeliver(ctx, "session:"+s.ID, ev)
}

// ListSessions 列出会话。
func (b *Backend) ListSessions() []*session.Session {
	all := b.sessions.List()
	out := make([]*session.Session, 0, len(all))
	for _, item := range all {
		if item.ParentID == "" {
			out = append(out, item)
		}
	}
	return out
}

// ChildSessions returns direct children of one parent session.
func (b *Backend) ChildSessions(parentID string) ([]*session.Session, error) {
	if _, ok := b.sessions.Get(parentID); !ok {
		return nil, session.ErrNotFound
	}
	all := b.sessions.List()
	out := make([]*session.Session, 0)
	for _, item := range all {
		if item.ParentID == parentID {
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

// CancelTurn 中断指定会话当前正在运行的回合(用户点停止)。
// 队列保留且暂停自动发送,由用户选择“立即发送”或再次发送后恢复。
func (b *Backend) CancelTurn(sessionID string) {
	b.cancelCurrentTurn(sessionID)
	b.mu.RLock()
	manager := b.subagents
	b.mu.RUnlock()
	if manager != nil {
		manager.CancelTree(sessionID)
	}
}

// ResolveApproval 回执一个审批决策(由客户端经 REST 触发)。
func (b *Backend) ResolveApproval(requestID string, decision string) error {
	d := approval.Decision(decision)
	return b.approval.Resolve(requestID, d)
}

// StartTerminal starts an interactive shell in the session project.
func (b *Backend) StartTerminal(
	ctx context.Context,
	sessionID string,
	cols, rows uint16,
) (terminal.Snapshot, error) {
	s, ok := b.sessions.Get(sessionID)
	if !ok {
		return terminal.Snapshot{}, session.ErrNotFound
	}
	projectPath := ""
	if s.ProjectID != "" {
		item, err := b.Project(s.ProjectID)
		if err != nil {
			return terminal.Snapshot{}, err
		}
		projectPath = item.Path
	}
	return b.terminal.Start(ctx, sessionID, projectPath, cols, rows)
}

// AttachTerminal returns the current recoverable terminal snapshot.
func (b *Backend) AttachTerminal(sessionID, ref string) (terminal.Snapshot, error) {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return terminal.Snapshot{}, session.ErrNotFound
	}
	return b.terminal.Attach(sessionID, ref)
}

// WriteTerminal forwards user input to an interactive shell.
func (b *Backend) WriteTerminal(sessionID, ref, input string) error {
	return b.terminal.Write(sessionID, ref, input)
}

// ResizeTerminal updates the PTY geometry.
func (b *Backend) ResizeTerminal(sessionID, ref string, cols, rows uint16) error {
	return b.terminal.Resize(sessionID, ref, cols, rows)
}

// StopTerminal terminates one interactive shell.
func (b *Backend) StopTerminal(sessionID, ref string) error {
	return b.terminal.Stop(sessionID, ref)
}

// SubscribeTerminal streams ordered PTY output independently from chat events.
func (b *Backend) SubscribeTerminal(
	ctx context.Context,
	sessionID, ref string,
	after uint64,
) (<-chan terminal.DataEvent, error) {
	return b.terminal.Subscribe(ctx, sessionID, ref, after)
}

// Subscribe 订阅某会话的事件流。
func (b *Backend) Subscribe(ctx context.Context, sessionID string) <-chan event.Event {
	return b.bus.Subscribe(ctx, "session:"+sessionID)
}

// History 返回某会话的对话历史。
func (b *Backend) History(ctx context.Context, sessionID string) ([]message.Message, error) {
	return b.log.History(ctx, sessionID)
}

// Replay 返回某会话中序号大于 after 的历史事件。
func (b *Backend) Replay(ctx context.Context, sessionID string, after event.Seq) ([]event.Event, error) {
	return b.log.Read(ctx, sessionID, after)
}

// Connections returns the configured Connection catalog in its user-defined order.
func (b *Backend) Connections() []config.Connection {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]config.Connection, 0, len(b.connections))
	for _, connection := range b.connections {
		out = append(out, connection)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SortOrder != out[j].SortOrder {
			return out[i].SortOrder < out[j].SortOrder
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func (b *Backend) Connection(id string) (config.Connection, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	connection, ok := b.connections[id]
	return connection, ok
}

// ListModels lists the catalog from one Connection's provider.
func (b *Backend) ListModels(ctx context.Context, connectionID string) ([]provider.ModelInfo, error) {
	b.mu.RLock()
	prov, ok := b.providers[connectionID]
	b.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrConnectionNotFound, connectionID)
	}
	lister, ok := prov.(provider.ModelLister)
	if !ok {
		return nil, fmt.Errorf("connection %q does not support model discovery", connectionID)
	}
	return lister.ListModels(ctx)
}

// Usage 返回会话最近一次模型请求的 token 使用情况。
func (b *Backend) Usage(ctx context.Context, sessionID string) (*provider.Usage, error) {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return nil, session.ErrNotFound
	}
	events, err := b.log.Read(ctx, sessionID, 0)
	if err != nil {
		return nil, err
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Kind != event.KindUsageUpdated {
			continue
		}
		switch usage := events[i].Payload.(type) {
		case provider.Usage:
			return &usage, nil
		case *provider.Usage:
			return usage, nil
		}
	}
	return nil, nil
}

// SetConnections replaces the Connection catalog and rebuilds provider clients.
// Existing Sessions keep their connection_id and therefore remain pinned to
// their original endpoint as long as that Connection remains configured.
func (b *Backend) SetConnections(connections []config.Connection) {
	nextConnections := make(map[string]config.Connection, len(connections))
	nextProviders := make(map[string]provider.Provider, len(connections))
	normalized := make([]config.Connection, 0, len(connections))
	sort.SliceStable(connections, func(i, j int) bool {
		if connections[i].LegacyDefault != connections[j].LegacyDefault {
			return connections[i].LegacyDefault
		}
		return connections[i].SortOrder < connections[j].SortOrder
	})
	for _, connection := range connections {
		if connection.ID == "" {
			continue
		}
		if connection.Kind == "" {
			connection.Kind = "openai"
		}
		if connection.AuthKind == "" {
			connection.AuthKind = "api_key"
		}
		if connection.Name == "" {
			connection.Name = connection.ID
		}
		normalized = append(normalized, connection)
	}
	for index := range normalized {
		connection := normalized[index]
		connection.SortOrder = index
		connection.LegacyDefault = false
		normalized[index] = connection
		nextConnections[connection.ID] = connection
		if b.buildProvider != nil {
			prov, _ := b.buildProvider(connection.Provider())
			nextProviders[connection.ID] = prov
		}
	}
	b.mu.Lock()
	b.connections = nextConnections
	b.providers = nextProviders
	b.firstConnectionID = ""
	if len(normalized) > 0 {
		b.firstConnectionID = normalized[0].ID
	}
	b.mu.Unlock()
	b.engine.SetProviderResolver(b.resolveSessionProvider)
	if err := config.SaveConnections(b.dataDir, normalized); err != nil {
		fmt.Fprintf(os.Stderr, "persist connections config failed: %v\n", err)
	}
}

func (b *Backend) CreateConnection(input config.Connection) (config.Connection, error) {
	if input.ContextWindow < 0 {
		return config.Connection{}, fmt.Errorf("context_window must be zero or greater")
	}
	if input.ID == "" {
		input.ID = newConnectionID()
	}
	if input.Name == "" {
		input.Name = input.ID
	}
	if input.Kind == "" {
		input.Kind = "openai"
	}
	if input.AuthKind == "" {
		input.AuthKind = "api_key"
	}
	if input.AuthKind != "api_key" {
		return config.Connection{}, fmt.Errorf("%w: %q", ErrUnsupportedAuth, input.AuthKind)
	}
	b.mu.Lock()
	if _, exists := b.connections[input.ID]; exists {
		b.mu.Unlock()
		return config.Connection{}, fmt.Errorf("connection %q already exists", input.ID)
	}
	b.mu.Unlock()
	connections := b.Connections()
	input.SortOrder = len(connections)
	connections = append(connections, input)
	b.SetConnections(connections)
	created, _ := b.Connection(input.ID)
	return created, nil
}

func (b *Backend) UpdateConnection(id string, patch config.Connection) (config.Connection, error) {
	current, exists := b.Connection(id)
	if !exists {
		return config.Connection{}, fmt.Errorf("%w: %q", ErrConnectionNotFound, id)
	}
	if patch.ContextWindow < 0 {
		return config.Connection{}, fmt.Errorf("context_window must be zero or greater")
	}
	patch.ID = id
	if patch.Name == "" {
		patch.Name = current.Name
	}
	if patch.Kind == "" {
		patch.Kind = current.Kind
	}
	if patch.AuthKind == "" {
		patch.AuthKind = current.AuthKind
	}
	if patch.AuthKind != "api_key" {
		return config.Connection{}, fmt.Errorf("%w: %q", ErrUnsupportedAuth, patch.AuthKind)
	}
	if patch.BaseURL == "" {
		patch.BaseURL = current.BaseURL
	}
	if patch.APIKey == "" {
		patch.APIKey = current.APIKey
	}
	if patch.DefaultModel == "" {
		patch.DefaultModel = current.DefaultModel
	}
	connections := b.Connections()
	targetIndex := patch.SortOrder
	if targetIndex < 0 {
		targetIndex = 0
	}
	if targetIndex >= len(connections) {
		targetIndex = len(connections) - 1
	}
	var updated config.Connection
	found := -1
	for index := range connections {
		if connections[index].ID == id {
			updated = patch
			found = index
			break
		}
	}
	if found >= 0 {
		connections = append(connections[:found], connections[found+1:]...)
		connections = append(connections, config.Connection{})
		copy(connections[targetIndex+1:], connections[targetIndex:])
		connections[targetIndex] = updated
	}
	b.SetConnections(connections)
	updated, _ = b.Connection(id)
	return updated, nil
}

func (b *Backend) DeleteConnection(id string) error {
	for _, sess := range b.sessions.List() {
		if sess.ConnectionID == id {
			return fmt.Errorf("%w: %q", ErrConnectionInUse, id)
		}
	}
	connections := b.Connections()
	next := make([]config.Connection, 0, len(connections))
	found := false
	for _, connection := range connections {
		if connection.ID == id {
			found = true
			continue
		}
		next = append(next, connection)
	}
	if !found {
		return fmt.Errorf("%w: %q", ErrConnectionNotFound, id)
	}
	b.SetConnections(next)
	return nil
}

func (b *Backend) resolveSessionProvider(sessionID string) (provider.Provider, string) {
	session, ok := b.sessions.Get(sessionID)
	if !ok || session.ConnectionID == "" {
		return nil, ""
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.providers[session.ConnectionID], b.connections[session.ConnectionID].DefaultModel
}

func newConnectionID() string {
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	return "conn_" + hex.EncodeToString(buf)
}
