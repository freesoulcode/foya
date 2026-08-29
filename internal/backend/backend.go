// Package backend 是传输无关的业务层:管理多连接、多会话、事件扇出、
// 实例级鉴权。它不关心底层是 Unix socket 还是 TCP,server 层把请求
// 转成对 Backend 的调用。
//
// 这是「一个内核多客户端」的落地关键:session 归 Backend 所有,多个
// 客户端连接可订阅同一 session,Backend 负责把事件扇出给所有订阅者。
package backend

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/freesoulcode/foya/internal/agent"
	"github.com/freesoulcode/foya/internal/approval"
	"github.com/freesoulcode/foya/internal/broker"
	"github.com/freesoulcode/foya/internal/config"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/provider"
	"github.com/freesoulcode/foya/internal/session"
	"github.com/freesoulcode/foya/internal/state"
	"github.com/freesoulcode/foya/internal/terminal"
)

// ProviderBuilder 按 provider 配置构造 provider 与默认模型名。
type ProviderBuilder func(config.Provider) (provider.Provider, string)

// Backend 是内核业务的统一入口(传输无关)。
type Backend struct {
	sessions session.Manager
	log      *state.MemLog
	bus      *broker.Broker[event.Event]
	engine   *agent.Engine
	approval approval.Gateway
	terminal terminal.Manager

	buildProvider ProviderBuilder
	dataDir       string
	mu            sync.RWMutex
	provCfg       config.Provider
	turns         *turnScheduler
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
		provCfg:       provCfg,
		turns:         newTurnScheduler(),
	}
}

// CreateSession 新建会话。
func (b *Backend) CreateSession(opts session.CreateOptions) (*session.Session, error) {
	return b.sessions.Create(opts)
}

// UpdateSession 局部更新会话可变字段。
func (b *Backend) UpdateSession(id string, model, workspace, approvalMode *string) (*session.Session, error) {
	return b.sessions.Update(id, model, workspace, approvalMode)
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
	b.stopSessionAndWait(id, deleteTurnGrace)
	b.terminal.CloseSession(id)
	if err := b.sessions.Delete(id); err != nil {
		return err
	}
	b.log.Delete(id)
	ev := event.Event{
		Kind:    event.KindSessionDeleted,
		Session: id,
		Time:    time.Now(),
		Payload: map[string]string{"id": id},
	}
	_ = b.bus.PublishMustDeliver(ctx, "session:"+id, ev)
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
	return b.sessions.List()
}

// CancelTurn 中断指定会话当前正在运行的回合(用户点停止)。
// 队列保留且暂停自动发送,由用户选择“立即发送”或再次发送后恢复。
func (b *Backend) CancelTurn(sessionID string) {
	b.cancelCurrentTurn(sessionID)
}

// ResolveApproval 回执一个审批决策(由客户端经 REST 触发)。
func (b *Backend) ResolveApproval(requestID string, decision string) {
	d := approval.Decision(decision)
	b.approval.Resolve(requestID, d)
}

// StartTerminal starts an interactive shell in the session workspace.
func (b *Backend) StartTerminal(
	ctx context.Context,
	sessionID string,
	cols, rows uint16,
) (terminal.Snapshot, error) {
	s, ok := b.sessions.Get(sessionID)
	if !ok {
		return terminal.Snapshot{}, session.ErrNotFound
	}
	return b.terminal.Start(ctx, sessionID, s.Workspace, cols, rows)
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

// ProviderConfig 返回当前 provider 配置。
func (b *Backend) ProviderConfig() config.Provider {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.provCfg
}

// ListModels 列出当前 provider 可用的模型及其上下文窗口。
func (b *Backend) ListModels(ctx context.Context) ([]provider.ModelInfo, error) {
	return b.engine.ListModels(ctx)
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

// SetProviderConfig 热替换 provider 配置并重建 provider。
func (b *Backend) SetProviderConfig(pc config.Provider) {
	prov, model := b.buildProvider(pc)
	b.engine.SwitchProvider(prov, model)
	b.mu.Lock()
	b.provCfg = pc
	b.mu.Unlock()
	if err := config.SaveProvider(b.dataDir, pc); err != nil {
		fmt.Fprintf(os.Stderr, "persist provider config failed: %v\n", err)
	}
}
