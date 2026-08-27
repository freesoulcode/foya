// Package backend 是传输无关的业务层:管理多连接、多会话、事件扇出、
// 实例级鉴权。它不关心底层是 Unix socket 还是 TCP,server 层把请求
// 转成对 Backend 的调用。
//
// 这是「一个内核多客户端」的落地关键:session 归 Backend 所有,多个
// 客户端连接可订阅同一 session,Backend 负责把事件扇出给所有订阅者。
package backend

import (
	"context"
	"sync"

	"github.com/freesoulcode/foya/internal/agent"
	"github.com/freesoulcode/foya/internal/broker"
	"github.com/freesoulcode/foya/internal/config"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/provider"
	"github.com/freesoulcode/foya/internal/session"
	"github.com/freesoulcode/foya/internal/state"
)

// ProviderBuilder 按 provider 配置构造 provider 与默认模型名。
// 由 kernel 注入,backend 借它在运行时热替换 provider。
type ProviderBuilder func(config.Provider) (provider.Provider, string)

// Backend 是内核业务的统一入口(传输无关)。
type Backend struct {
	sessions session.Manager
	log      *state.MemLog
	bus      *broker.Broker[event.Event]
	engine   *agent.Engine

	buildProvider ProviderBuilder
	mu            sync.RWMutex // 保护 provCfg
	provCfg       config.Provider
}

// New 组装一个 Backend。
func New(sessions session.Manager, log *state.MemLog, bus *broker.Broker[event.Event], engine *agent.Engine, build ProviderBuilder, provCfg config.Provider) *Backend {
	return &Backend{
		sessions:      sessions,
		log:           log,
		bus:           bus,
		engine:        engine,
		buildProvider: build,
		provCfg:       provCfg,
	}
}

// CreateSession 新建会话。
func (b *Backend) CreateSession(model string) (*session.Session, error) {
	return b.sessions.Create(model)
}

// ListSessions 列出会话。
func (b *Backend) ListSessions() []*session.Session {
	return b.sessions.List()
}

// SubmitTurn 提交一轮对话(同步执行,事件通过 SSE 流出)。
func (b *Backend) SubmitTurn(ctx context.Context, sessionID, text string) error {
	return b.engine.RunTurn(ctx, sessionID, text)
}

// Subscribe 订阅某会话的事件流(供 SSE)。
func (b *Backend) Subscribe(ctx context.Context, sessionID string) <-chan event.Event {
	return b.bus.Subscribe(ctx, "session:"+sessionID)
}

// History 返回某会话的对话历史(从事件日志投影)。
func (b *Backend) History(ctx context.Context, sessionID string) ([]message.Message, error) {
	return b.log.History(ctx, sessionID)
}

// Replay 返回某会话中序号大于 after 的历史事件(供 SSE 断线补发)。
func (b *Backend) Replay(ctx context.Context, sessionID string, after event.Seq) ([]event.Event, error) {
	return b.log.Read(ctx, sessionID, after)
}

// ProviderConfig 返回当前 provider 配置(供设置界面读取)。
func (b *Backend) ProviderConfig() config.Provider {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.provCfg
}

// SetProviderConfig 热替换 provider 配置并重建 provider(供设置界面保存)。
func (b *Backend) SetProviderConfig(pc config.Provider) {
	prov, model := b.buildProvider(pc)
	b.engine.SwitchProvider(prov, model)
	b.mu.Lock()
	b.provCfg = pc
	b.mu.Unlock()
}
