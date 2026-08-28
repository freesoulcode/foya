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

	buildProvider ProviderBuilder
	dataDir       string
	mu            sync.RWMutex
	provCfg       config.Provider
}

// New 组装一个 Backend。
func New(
	sessions session.Manager,
	log *state.MemLog,
	bus *broker.Broker[event.Event],
	engine *agent.Engine,
	gw approval.Gateway,
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
		buildProvider: build,
		dataDir:       dataDir,
		provCfg:       provCfg,
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
	ev := event.Event{Kind: event.KindSessionUpdated, Session: id, Time: time.Now(), Payload: s}
	seq, _ := b.log.Append(ctx, ev)
	ev.Seq = seq
	_ = b.bus.PublishMustDeliver(ctx, "session:"+id, ev)
	return s, nil
}

// ListSessions 列出会话。
func (b *Backend) ListSessions() []*session.Session {
	return b.sessions.List()
}

// SubmitTurn 提交一轮对话。
func (b *Backend) SubmitTurn(ctx context.Context, sessionID, text string) error {
	return b.engine.RunTurn(ctx, sessionID, text)
}

// CancelTurn 中断指定会话当前正在运行的回合(用户点停止)。
func (b *Backend) CancelTurn(sessionID string) {
	b.engine.Cancel(sessionID)
}

// ResolveApproval 回执一个审批决策(由客户端经 REST 触发)。
func (b *Backend) ResolveApproval(requestID string, decision string) {
	d := approval.Decision(decision)
	b.approval.Resolve(requestID, d)
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

// ListModels 列出当前 provider 可用的模型。
func (b *Backend) ListModels(ctx context.Context) ([]string, error) {
	return b.engine.ListModels(ctx)
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
