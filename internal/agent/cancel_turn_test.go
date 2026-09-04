package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/freesoulcode/foya/internal/approval"
	"github.com/freesoulcode/foya/internal/broker"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/provider"
	"github.com/freesoulcode/foya/internal/session"
	"github.com/freesoulcode/foya/internal/tool"
)

type cancelErrorProvider struct {
	started chan struct{}
}

func (p *cancelErrorProvider) Name() string { return "cancel-error" }

func (p *cancelErrorProvider) Stream(ctx context.Context, _ provider.Request) (<-chan provider.StreamEvent, error) {
	out := make(chan provider.StreamEvent, 2)
	go func() {
		defer close(out)
		out <- provider.StreamEvent{Type: "reasoning_delta", Text: "found the persistence boundary"}
		close(p.started)
		<-ctx.Done()
		out <- provider.StreamEvent{Type: "error", Text: ctx.Err().Error()}
	}()
	return out, nil
}

type toolCallProvider struct{}

func (p *toolCallProvider) Name() string { return "tool-call" }

func (p *toolCallProvider) Stream(context.Context, provider.Request) (<-chan provider.StreamEvent, error) {
	out := make(chan provider.StreamEvent, 2)
	out <- provider.StreamEvent{
		Type:        "tool_call_delta",
		ToolIndex:   0,
		ToolCallID:  "call-1",
		ToolName:    "sequential_test",
		ToolArgsDlt: `{}`,
	}
	out <- provider.StreamEvent{Type: "done", FinishReason: "tool_calls"}
	close(out)
	return out, nil
}

func TestCancelledTurnPersistsPartialReasoning(t *testing.T) {
	sessions := newTestSessionManager(t)
	sess, err := sessions.Create(session.CreateOptions{Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	log := newTestStore(t)
	bus := broker.New[event.Event]()
	gateway := approval.NewGateway(bus, log)
	prov := &cancelErrorProvider{started: make(chan struct{})}
	engine := NewEngine(log, bus, sessions, prov, "test-model", tool.NewRegistry(), gateway)

	done := make(chan error, 1)
	go func() {
		done <- engine.RunTurn(context.Background(), sess.ID, "research")
	}()
	select {
	case <-prov.started:
	case <-time.After(time.Second):
		t.Fatal("provider did not start")
	}
	engine.Cancel(sess.ID)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("turn did not stop")
	}

	history, err := log.History(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	var cancelled *message.Message
	for i := range history {
		if history[i].Role == message.RoleAssistant && history[i].TurnStatus == "cancelled" {
			cancelled = &history[i]
			break
		}
	}
	if cancelled == nil || !strings.Contains(cancelled.Reasoning, "persistence boundary") {
		t.Fatalf("cancelled partial reasoning was not persisted: %#v", history)
	}
	modelMessages := provider.TextMessages(history)
	if got := modelMessages[len(modelMessages)-1].Parts[0].Text; !strings.Contains(got, "persistence boundary") {
		t.Fatalf("cancelled reasoning was not projected into model context: %q", got)
	}
}

func TestCancelledToolCallPersistsInterruptedResult(t *testing.T) {
	sessions := newTestSessionManager(t)
	sess, err := sessions.Create(session.CreateOptions{Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	log := newTestStore(t)
	bus := broker.New[event.Event]()
	gateway := approval.NewGateway(bus, log)
	registry := tool.NewRegistry()
	blocking := &blockingSequentialTool{
		started: make(chan string, 1),
		release: make(chan struct{}),
	}
	registry.Register(blocking)
	engine := NewEngine(log, bus, sessions, &toolCallProvider{}, "test-model", registry, gateway)

	done := make(chan error, 1)
	go func() {
		done <- engine.RunTurn(context.Background(), sess.ID, "write")
	}()
	select {
	case <-blocking.started:
	case <-time.After(time.Second):
		t.Fatal("tool did not start")
	}
	engine.Cancel(sess.ID)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("turn did not stop")
	}

	history, err := log.History(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	var toolResult *message.Message
	for i := range history {
		if history[i].Role == message.RoleTool && history[i].ToolCallID == "call-1" {
			toolResult = &history[i]
			break
		}
	}
	if toolResult == nil || toolResult.Content != "已中断" {
		t.Fatalf("interrupted tool result was not persisted: %#v", history)
	}
}
