// Package approval 是审批网关:工具执行前决定自动放行 / 问用户 / 拒绝。
//
// 内核内部用 channel 同步阻塞实现,对外表现为「SSE 出请求 + REST 回决策」
// 的异步对,靠 requestID 关联。多客户端场景下,任一端(如手机)回决策,
// 内核解除阻塞并广播结果,其它端(如桌面)自动同步。
package approval

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/freesoulcode/foya/internal/broker"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/state"
)

// Mode 是审批档位。
type Mode string

const (
	ModeExplore Mode = "explore" // 只读探索,不问
	ModeAsk     Mode = "ask"     // 危险操作询问
	ModeBypass  Mode = "bypass"  // 不问(信任自动化)
)

// Decision 是审批决策。
type Decision string

const (
	DecisionAutoApprove        Decision = "auto_approve"
	DecisionApproved           Decision = "approved"
	DecisionApprovedForSession Decision = "approved_for_session"
	DecisionDenied             Decision = "denied"
)

// Request 是一次审批请求。
type Request struct {
	ID       string `json:"id"`
	Session  string `json:"session"`
	ToolName string `json:"tool_name"`
	Action   string `json:"action"` // read / write / execute
	Detail   string `json:"detail"`
}

// Gateway 是审批网关。
type Gateway interface {
	// Request 由工具内部就地调用;内部阻塞直到收到决策或 ctx 取消。
	Request(ctx context.Context, req Request) (Decision, error)
	// Resolve 由客户端经 REST 回执触发;按 requestID 解除阻塞。
	// 采用 take 语义:同一请求只有第一个决策生效(多端竞争安全)。
	Resolve(requestID string, d Decision)
}

// ctxKey 是审批相关上下文值的键类型。
type ctxKey int

const (
	ctxKeyMode    ctxKey = iota
	ctxKeySession
)

// WithMode 把审批档位注入上下文。
func WithMode(ctx context.Context, mode Mode) context.Context {
	return context.WithValue(ctx, ctxKeyMode, mode)
}

// WithSession 把会话 ID 注入上下文。
func WithSession(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, ctxKeySession, sessionID)
}

// gateway 是 Gateway 的内存实现。
type gateway struct {
	mu      sync.Mutex
	pending map[string]chan Decision
	bus     *broker.Broker[event.Event]
	log     *state.MemLog
}

// NewGateway 创建内存版审批网关。
func NewGateway(bus *broker.Broker[event.Event], log *state.MemLog) Gateway {
	return &gateway{
		pending: make(map[string]chan Decision),
		bus:     bus,
		log:     log,
	}
}

// Request 由工具内部调用。根据审批档位决定自动放行/拒绝/等待用户。
func (g *gateway) Request(ctx context.Context, req Request) (Decision, error) {
	mode := modeFromContext(ctx)

	switch mode {
	case ModeBypass:
		return DecisionAutoApprove, nil
	case ModeExplore:
		if req.Action == "read" {
			return DecisionAutoApprove, nil
		}
		return DecisionDenied, nil
	case ModeAsk:
		// 继续,等待用户决策
	default:
	}

	// 补全请求元数据。
	if req.ID == "" {
		req.ID = newID()
	}
	if sess, ok := ctx.Value(ctxKeySession).(string); ok {
		req.Session = sess
	}

	ch := make(chan Decision, 1)
	g.mu.Lock()
	g.pending[req.ID] = ch
	g.mu.Unlock()

	ev := event.Event{
		Kind:    event.KindApprovalReq,
		Session: req.Session,
		Time:    time.Now(),
		Payload: req,
	}
	seq, _ := g.log.Append(ctx, ev)
	ev.Seq = seq
	_ = g.bus.PublishMustDeliver(ctx, "session:"+req.Session, ev)

	select {
	case d := <-ch:
		resolvedEv := event.Event{
			Kind:    event.KindApprovalResolved,
			Session: req.Session,
			Time:    time.Now(),
			Payload: struct {
				ID       string `json:"id"`
				Decision Decision `json:"decision"`
			}{req.ID, d},
		}
		seq2, _ := g.log.Append(ctx, resolvedEv)
		resolvedEv.Seq = seq2
		_ = g.bus.PublishMustDeliver(ctx, "session:"+req.Session, resolvedEv)
		return d, nil
	case <-ctx.Done():
		g.mu.Lock()
		delete(g.pending, req.ID)
		g.mu.Unlock()
		return DecisionDenied, ctx.Err()
	}
}

// Resolve 由客户端经 REST 回执触发。take 语义:只有第一个决策生效。
func (g *gateway) Resolve(requestID string, d Decision) {
	g.mu.Lock()
	ch, ok := g.pending[requestID]
	if ok {
		delete(g.pending, requestID)
	}
	g.mu.Unlock()
	if !ok {
		return
	}
	ch <- d
}

func modeFromContext(ctx context.Context) Mode {
	if m, ok := ctx.Value(ctxKeyMode).(Mode); ok {
		return m
	}
	return ModeAsk
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
