package approval

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/freesoulcode/foya/internal/broker"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/testkit"
)

type staticReviewer struct {
	review Review
	err    error
	calls  int
}

func (r *staticReviewer) Review(context.Context, Request) (Review, error) {
	r.calls++
	return r.review, r.err
}

func newTestGateway(t testing.TB) Gateway {
	return NewGateway(broker.New[event.Event](), testkit.NewLog())
}

func TestGatewayAutomaticallyApprovesReads(t *testing.T) {
	reviewer := &staticReviewer{err: errors.New("must not be called")}
	ctx := WithReviewer(WithMode(context.Background(), ModeAuto), reviewer)
	decision, err := newTestGateway(t).Request(ctx, Request{Action: "read"})
	if err != nil || decision != DecisionAutoApprove {
		t.Fatalf("decision = %q, err = %v", decision, err)
	}
	if reviewer.calls != 0 {
		t.Fatalf("reviewer calls = %d, want 0", reviewer.calls)
	}
}

func TestGatewayUsesGuardianInAutoMode(t *testing.T) {
	for _, test := range []struct {
		name     string
		review   Review
		err      error
		decision Decision
		wantErr  bool
	}{
		{name: "approve", review: Review{Approved: true}, decision: DecisionAutoApprove},
		{name: "deny", review: Review{Approved: false}, decision: DecisionDenied},
		{name: "error", err: errors.New("offline"), decision: DecisionDenied, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			reviewer := &staticReviewer{review: test.review, err: test.err}
			ctx := WithReviewer(WithMode(context.Background(), ModeAuto), reviewer)
			decision, err := newTestGateway(t).Request(ctx, Request{Action: "execute"})
			if decision != test.decision || (err != nil) != test.wantErr {
				t.Fatalf("decision = %q, err = %v", decision, err)
			}
		})
	}
}

func TestGuardianApprovalIsNotCached(t *testing.T) {
	gateway := newTestGateway(t)
	request := Request{
		ToolName: "bash",
		Action:   "execute",
		Resource: "go test ./...",
		Scope:    "/workspace",
	}
	autoCtx := WithSession(
		WithReviewer(WithMode(context.Background(), ModeAuto), &staticReviewer{
			review: Review{Approved: true},
		}),
		"session-1",
	)
	if decision, err := gateway.Request(autoCtx, request); err != nil || decision != DecisionAutoApprove {
		t.Fatalf("guardian decision = %q, err = %v", decision, err)
	}

	manualCtx, cancel := context.WithTimeout(
		WithSession(WithMode(context.Background(), ModeManual), "session-1"),
		20*time.Millisecond,
	)
	defer cancel()
	if decision, err := gateway.Request(manualCtx, request); decision != DecisionDenied ||
		!errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("manual decision = %q, err = %v", decision, err)
	}
}

func TestGatewaySessionGrantIsScopedAndClearable(t *testing.T) {
	gateway := newTestGateway(t)
	request := Request{
		ID:       "request-1",
		ToolName: "write",
		Action:   "write",
		Resource: "/workspace/a.txt",
		Scope:    "/workspace",
	}
	ctx := WithSession(WithMode(context.Background(), ModeManual), "session-1")
	result := make(chan Decision, 1)
	go func() {
		decision, _ := gateway.Request(ctx, request)
		result <- decision
	}()
	waitForPending(t, gateway, request.ID)
	if err := gateway.Resolve(request.ID, DecisionApprovedForSession); err != nil {
		t.Fatal(err)
	}
	if decision := <-result; decision != DecisionApprovedForSession {
		t.Fatalf("decision = %q", decision)
	}

	decision, err := gateway.Request(ctx, Request{
		ToolName: "write",
		Action:   "write",
		Resource: "/workspace/b.txt",
		Scope:    "/workspace",
	})
	if err != nil || decision != DecisionApprovedForSession {
		t.Fatalf("cached decision = %q, err = %v", decision, err)
	}

	assertManualApprovalRequired(t, gateway, "session-2", "/workspace")
	assertManualApprovalRequired(t, gateway, "session-1", "/other-workspace")

	gateway.ClearSession("session-1")
	assertManualApprovalRequired(t, gateway, "session-1", "/workspace")
}

func assertManualApprovalRequired(t *testing.T, gateway Gateway, sessionID, scope string) {
	t.Helper()
	timeoutCtx, cancel := context.WithTimeout(
		WithSession(WithMode(context.Background(), ModeManual), sessionID),
		20*time.Millisecond,
	)
	defer cancel()
	decision, err := gateway.Request(timeoutCtx, Request{
		ToolName: "write",
		Action:   "write",
		Resource: "/workspace/c.txt",
		Scope:    scope,
	})
	if decision != DecisionDenied || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("session %q scope %q decision = %q, err = %v", sessionID, scope, decision, err)
	}
}

func TestGatewayRejectsLegacyModesAndInvalidDecisions(t *testing.T) {
	gateway := newTestGateway(t)
	decision, err := gateway.Request(
		WithMode(context.Background(), Mode("ask")),
		Request{Action: "execute"},
	)
	if decision != DecisionDenied || !errors.Is(err, ErrInvalidMode) {
		t.Fatalf("decision = %q, err = %v", decision, err)
	}
	if err := gateway.Resolve("request", Decision("allow")); !errors.Is(err, ErrInvalidDecision) {
		t.Fatalf("err = %v, want ErrInvalidDecision", err)
	}
}

func waitForPending(t *testing.T, gw Gateway, requestID string) {
	t.Helper()
	concrete := gw.(*gateway)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		concrete.mu.Lock()
		_, ok := concrete.pending[requestID]
		concrete.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("approval request did not become pending")
}
