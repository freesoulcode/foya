// Package approval decides whether a tool runs automatically, asks the user, or is denied.
//
// Internally, a channel blocks execution. Externally, an SSE request and REST
// decision form an asynchronous pair keyed by request ID. Any connected client
// can resolve the request and the result is broadcast to the others.
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
	foyatelemetry "github.com/freesoulcode/foya/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Mode is an approval policy.
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

// Decision is the outcome of an approval request.
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

// Request describes one approval request.
type Request struct {
	ID               string `json:"id"`
	Session          string `json:"session"`
	ExecutionSession string `json:"execution_session,omitempty"`
	ToolName         string `json:"tool_name"`
	Action           string `json:"action"` // read / write / delete / execute / network
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

// Gateway coordinates approval requests.
type Gateway interface {
	// Request blocks until a decision arrives or the context is cancelled.
	Request(ctx context.Context, req Request) (Decision, error)
	// Resolve unblocks a request by ID. Only the first decision takes effect.
	Resolve(requestID string, d Decision) error
	ClearSession(sessionID string)
}

// ctxKey identifies approval values stored in a context.
type ctxKey int

const (
	ctxKeyMode ctxKey = iota
	ctxKeySession
	ctxKeyExecutionSession
	ctxKeyReviewer
	ctxKeyNotification
	ctxKeyRequestHook
)

// WithMode stores an approval mode in a context.
func WithMode(ctx context.Context, mode Mode) context.Context {
	return context.WithValue(ctx, ctxKeyMode, mode)
}

// WithSession stores a chat ID in a context.
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

// RequestHook can resolve an approval before the built-in reviewer or user
// prompt runs. Returning handled=false preserves the normal approval flow.
type RequestHook func(context.Context, Request) (decision Decision, handled bool)

func WithRequestHook(ctx context.Context, hook RequestHook) context.Context {
	return context.WithValue(ctx, ctxKeyRequestHook, hook)
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

// gateway is the in-memory Gateway implementation.
type gateway struct {
	mu      sync.Mutex
	pending map[string]pendingRequest
	grants  map[grantKey]struct{}
	bus     *broker.Broker[event.Event]
	log     state.Log
}

// NewGateway creates an in-memory approval gateway.
func NewGateway(bus *broker.Broker[event.Event], log state.Log) Gateway {
	return &gateway{
		pending: make(map[string]pendingRequest),
		grants:  make(map[grantKey]struct{}),
		bus:     bus,
		log:     log,
	}
}

// Request applies the approval mode and may wait for user input.
func (g *gateway) Request(ctx context.Context, req Request) (decision Decision, resultErr error) {
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
	startedAt := time.Now()
	approvalAttrs := []attribute.KeyValue{
		attribute.String("session.id", req.ExecutionSession),
		attribute.String("langfuse.session.id", req.Session),
		attribute.String("langfuse.trace.name", "foya.turn"),
		attribute.String("langfuse.observation.type", "span"),
		attribute.String("foya.approval.id", req.ID),
		attribute.String("foya.approval.tool", req.ToolName),
		attribute.String("foya.approval.action", req.Action),
	}
	ctx, approvalSpan := foyatelemetry.StartSpan(
		ctx,
		"foya.approval.wait",
		trace.SpanKindInternal,
		approvalAttrs...,
	)
	defer func() {
		finalAttrs := append(
			[]attribute.KeyValue(nil),
			approvalAttrs...,
		)
		finalAttrs = append(finalAttrs, attribute.String("foya.approval.decision", string(decision)))
		status := "completed"
		if resultErr != nil || decision == DecisionDenied {
			status = "failed"
		}
		foyatelemetry.EndSpan(approvalSpan, status, resultErr, finalAttrs...)
		foyatelemetry.RecordApproval(
			ctx,
			time.Since(startedAt),
			attribute.String("foya.approval.tool", req.ToolName),
			attribute.String("foya.approval.action", req.Action),
			attribute.String("foya.approval.decision", string(decision)),
		)
	}()
	if hook, ok := ctx.Value(ctxKeyRequestHook).(RequestHook); ok && hook != nil {
		if decision, handled := hook(ctx, req); handled {
			if decision != DecisionAutoApprove && decision != DecisionDenied {
				return DecisionDenied, fmt.Errorf("%w from permission hook: %q", ErrInvalidDecision, decision)
			}
			return decision, nil
		}
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

// Resolve applies the first client decision for a pending request.
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
