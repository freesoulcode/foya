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
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/freesoulcode/foya/internal/broker"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/state"
)

// Mode 是审批档位。
type Mode string

const (
	ModeManual     Mode = "manual"
	ModeAuto       Mode = "auto"
	ModeFullAccess Mode = "full_access"
)

func ValidMode(mode Mode) bool {
	switch mode {
	case ModeManual, ModeAuto, ModeFullAccess:
		return true
	default:
		return false
	}
}

// Decision 是审批决策。
type Decision string

const (
	DecisionAutoApprove        Decision = "auto_approve"
	DecisionApproved           Decision = "approved"
	DecisionApprovedForSession Decision = "approved_for_session"
	DecisionDenied             Decision = "denied"
)

var (
	ErrInvalidMode         = errors.New("invalid approval mode")
	ErrInvalidDecision     = errors.New("invalid approval decision")
	ErrGuardianUnavailable = errors.New("guardian is unavailable")
)

// Request 是一次审批请求。
type Request struct {
	ID               string `json:"id"`
	Session          string `json:"session"`
	ExecutionSession string `json:"execution_session,omitempty"`
	ToolName         string `json:"tool_name"`
	Action           string `json:"action"` // read / write / execute / network
	Detail           string `json:"detail"`
	Resource         string `json:"resource,omitempty"`
	Scope            string `json:"scope,omitempty"`
}

type Review struct {
	Approved bool
	Reason   string
}

type Reviewer interface {
	Review(context.Context, Request) (Review, error)
}

// Gateway 是审批网关。
type Gateway interface {
	// Request 由工具内部就地调用;内部阻塞直到收到决策或 ctx 取消。
	Request(ctx context.Context, req Request) (Decision, error)
	// Resolve 由客户端经 REST 回执触发;按 requestID 解除阻塞。
	// 采用 take 语义:同一请求只有第一个决策生效(多端竞争安全)。
	Resolve(requestID string, d Decision) error
	ClearSession(sessionID string)
}

// ctxKey 是审批相关上下文值的键类型。
type ctxKey int

const (
	ctxKeyMode ctxKey = iota
	ctxKeySession
	ctxKeyExecutionSession
	ctxKeyReviewer
	ctxKeyNotification
)

// WithMode 把审批档位注入上下文。
func WithMode(ctx context.Context, mode Mode) context.Context {
	return context.WithValue(ctx, ctxKeyMode, mode)
}

// WithSession 把会话 ID 注入上下文。
func WithSession(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, ctxKeySession, sessionID)
}

// WithExecutionSession records the child session performing the action when
// approval UI is intentionally routed through its parent session.
func WithExecutionSession(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, ctxKeyExecutionSession, sessionID)
}

func WithReviewer(ctx context.Context, reviewer Reviewer) context.Context {
	return context.WithValue(ctx, ctxKeyReviewer, reviewer)
}

// NotificationHandler is called after a manual approval request becomes
// visible to clients. It must return quickly; notification delivery itself is
// asynchronous and must never delay the approval flow.
type NotificationHandler func(context.Context, Request)

// WithNotificationHandler installs an optional notification callback for the
// current tool execution.
func WithNotificationHandler(ctx context.Context, handler NotificationHandler) context.Context {
	return context.WithValue(ctx, ctxKeyNotification, handler)
}

type pendingRequest struct {
	request Request
	result  chan Decision
}

type grantKey struct {
	session  string
	tool     string
	action   string
	resource string
}

// gateway 是 Gateway 的内存实现。
type gateway struct {
	mu      sync.Mutex
	pending map[string]pendingRequest
	grants  map[grantKey]struct{}
	bus     *broker.Broker[event.Event]
	log     state.Log
}

// NewGateway 创建内存版审批网关。
func NewGateway(bus *broker.Broker[event.Event], log state.Log) Gateway {
	return &gateway{
		pending: make(map[string]pendingRequest),
		grants:  make(map[grantKey]struct{}),
		bus:     bus,
		log:     log,
	}
}

// Request 由工具内部调用。根据审批档位决定自动放行/拒绝/等待用户。
func (g *gateway) Request(ctx context.Context, req Request) (Decision, error) {
	mode := ModeFromContext(ctx)
	if !ValidMode(mode) {
		return DecisionDenied, fmt.Errorf("%w: %q", ErrInvalidMode, mode)
	}
	if req.ID == "" {
		req.ID = newID()
	}
	if sess, ok := ctx.Value(ctxKeySession).(string); ok {
		req.Session = sess
	}
	if sess, ok := ctx.Value(ctxKeyExecutionSession).(string); ok {
		req.ExecutionSession = sess
	}

	if mode == ModeFullAccess || req.Action == "read" {
		return DecisionAutoApprove, nil
	}
	if g.hasGrant(req) {
		return DecisionApprovedForSession, nil
	}

	if mode == ModeAuto {
		reviewer, ok := ctx.Value(ctxKeyReviewer).(Reviewer)
		if !ok || reviewer == nil {
			return DecisionDenied, ErrGuardianUnavailable
		}
		review, err := reviewer.Review(ctx, req)
		if err != nil {
			return DecisionDenied, fmt.Errorf("guardian review: %w", err)
		}
		if review.Approved {
			return DecisionAutoApprove, nil
		}
		return DecisionDenied, nil
	}

	ch := make(chan Decision, 1)
	g.mu.Lock()
	g.pending[req.ID] = pendingRequest{request: req, result: ch}
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
	if notify, ok := ctx.Value(ctxKeyNotification).(NotificationHandler); ok && notify != nil {
		notify(context.WithoutCancel(ctx), req)
	}

	select {
	case d := <-ch:
		resolvedEv := event.Event{
			Kind:    event.KindApprovalResolved,
			Session: req.Session,
			Time:    time.Now(),
			Payload: struct {
				ID       string   `json:"id"`
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
func (g *gateway) Resolve(requestID string, d Decision) error {
	if d != DecisionApproved && d != DecisionApprovedForSession && d != DecisionDenied {
		return fmt.Errorf("%w: %q", ErrInvalidDecision, d)
	}
	g.mu.Lock()
	pending, ok := g.pending[requestID]
	if ok {
		delete(g.pending, requestID)
		if d == DecisionApprovedForSession && pending.request.Session != "" {
			g.grants[requestGrantKey(pending.request)] = struct{}{}
		}
	}
	g.mu.Unlock()
	if !ok {
		return nil
	}
	pending.result <- d
	return nil
}

func (g *gateway) ClearSession(sessionID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for key := range g.grants {
		if key.session == sessionID {
			delete(g.grants, key)
		}
	}
}

func (g *gateway) hasGrant(req Request) bool {
	if req.Session == "" {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.grants[requestGrantKey(req)]
	return ok
}

func requestGrantKey(req Request) grantKey {
	resource := req.Scope
	if resource == "" {
		resource = req.Resource
	}
	if resource == "" {
		resource = req.Detail
	}
	return grantKey{
		session:  req.Session,
		tool:     req.ToolName,
		action:   req.Action,
		resource: resource,
	}
}

func ModeFromContext(ctx context.Context) Mode {
	if m, ok := ctx.Value(ctxKeyMode).(Mode); ok {
		return m
	}
	return ModeManual
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
