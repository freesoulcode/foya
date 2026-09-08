package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/freesoulcode/foya/internal/broker"
	conversation "github.com/freesoulcode/foya/internal/conversation"
	interaction "github.com/freesoulcode/foya/internal/interaction"

	modelapi "github.com/freesoulcode/foya/internal/model"

	"github.com/freesoulcode/foya/internal/tool"
)

type cancelErrorProvider struct {
	started chan struct{}
}

func (p *cancelErrorProvider) Name() string { return "cancel-error" }

func (p *cancelErrorProvider) Stream(ctx context.Context, _ modelapi.Request) (<-chan modelapi.StreamEvent, error) {
	out := make(chan modelapi.StreamEvent, 2)
	go func() {
		defer close(out)
		out <- modelapi.StreamEvent{Type: "reasoning_delta", Text: "found the persistence boundary"}
		close(p.started)
		<-ctx.Done()
		out <- modelapi.StreamEvent{Type: "error", Text: ctx.Err().Error()}
	}()
	return out, nil
}

type toolCallProvider struct{}

func (p *toolCallProvider) Name() string { return "tool-call" }

func (p *toolCallProvider) Stream(context.Context, modelapi.Request) (<-chan modelapi.StreamEvent, error) {
	out := make(chan modelapi.StreamEvent, 2)
	out <- modelapi.StreamEvent{
		Type:        "tool_call_delta",
		ToolIndex:   0,
		ToolCallID:  "call-1",
		ToolName:    "sequential_test",
		ToolArgsDlt: `{}`,
	}
	out <- modelapi.StreamEvent{Type: "done", FinishReason: "tool_calls"}
	close(out)
	return out, nil
}

func TestCancelledTurnPersistsPartialReasoning(t *testing.T) {
	sessions := newTestSessionManager(t)
	sess, err := sessions.Create(conversation.CreateOptions{Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	log := newTestStore(t)
	bus := broker.New[conversation.Event]()
	gateway := interaction.NewGateway(bus, log)
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
	var cancelled *conversation.Message
	for i := range history {
		if history[i].Role == conversation.RoleAssistant && history[i].TurnStatus == "cancelled" {
			cancelled = &history[i]
			break
		}
	}
	if cancelled == nil || !strings.Contains(cancelled.Reasoning, "persistence boundary") {
		t.Fatalf("cancelled partial reasoning was not persisted: %#v", history)
	}
	modelMessages := modelMessages(history)
	if got := modelMessages[len(modelMessages)-1].Parts[0].Text; !strings.Contains(got, "persistence boundary") {
		t.Fatalf("cancelled reasoning was not projected into model context: %q", got)
	}
}

func TestCancelledToolCallPersistsInterruptedResult(t *testing.T) {
	sessions := newTestSessionManager(t)
	sess, err := sessions.Create(conversation.CreateOptions{Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	log := newTestStore(t)
	bus := broker.New[conversation.Event]()
	gateway := interaction.NewGateway(bus, log)
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
	var toolResult *conversation.Message
	for i := range history {
		if history[i].Role == conversation.RoleTool && history[i].ToolCallID == "call-1" {
			toolResult = &history[i]
			break
		}
	}
	if toolResult == nil || toolResult.Content != "Interrupted" {
		t.Fatalf("interrupted tool result was not persisted: %#v", history)
	}
}
