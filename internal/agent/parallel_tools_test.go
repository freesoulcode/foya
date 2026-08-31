package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/freesoulcode/foya/internal/approval"
	"github.com/freesoulcode/foya/internal/broker"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/provider"
	"github.com/freesoulcode/foya/internal/session"
	"github.com/freesoulcode/foya/internal/state"
	"github.com/freesoulcode/foya/internal/tool"
)

type parallelProvider struct {
	mu    sync.Mutex
	calls int
}

func (p *parallelProvider) Name() string { return "parallel-test" }

func (p *parallelProvider) ListModels(context.Context) ([]provider.ModelInfo, error) {
	return []provider.ModelInfo{{ID: "test-model", ContextWindow: 100_000}}, nil
}

func (p *parallelProvider) Stream(
	context.Context,
	provider.Request,
) (<-chan provider.StreamEvent, error) {
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.mu.Unlock()
	out := make(chan provider.StreamEvent, 4)
	if call == 1 {
		out <- provider.StreamEvent{
			Type: "tool_call_delta", ToolIndex: 0,
			ToolCallID: "call-1", ToolName: "parallel_test", ToolArgsDlt: `{}`,
		}
		out <- provider.StreamEvent{
			Type: "tool_call_delta", ToolIndex: 1,
			ToolCallID: "call-2", ToolName: "parallel_test", ToolArgsDlt: `{}`,
		}
		out <- provider.StreamEvent{Type: "done", FinishReason: "tool_calls"}
	} else {
		out <- provider.StreamEvent{Type: "done", FinishReason: "stop"}
	}
	close(out)
	return out, nil
}

type blockingParallelTool struct {
	started chan struct{}
	release chan struct{}
}

type blockingSequentialTool struct {
	started chan string
	release chan struct{}
}

type captureProvider struct {
	request provider.Request
}

func (p *captureProvider) Name() string { return "capture" }
func (p *captureProvider) Stream(
	_ context.Context,
	request provider.Request,
) (<-chan provider.StreamEvent, error) {
	p.request = request
	out := make(chan provider.StreamEvent, 1)
	out <- provider.StreamEvent{Type: "done", FinishReason: "stop"}
	close(out)
	return out, nil
}

type staticTool struct{ name string }

func (t staticTool) Name() string            { return t.name }
func (t staticTool) Description() string     { return t.name }
func (t staticTool) Spec() []byte            { return []byte(`{"type":"object"}`) }
func (t staticTool) Exposure() tool.Exposure { return tool.ExposureDirect }
func (t staticTool) Run(context.Context, tool.Call) (tool.Result, error) {
	return tool.Result{Content: []tool.ContentPart{{Type: "text", Text: "ok"}}}, nil
}

func (t *blockingParallelTool) Name() string            { return "parallel_test" }
func (t *blockingParallelTool) Description() string     { return "test" }
func (t *blockingParallelTool) Spec() []byte            { return []byte(`{"type":"object"}`) }
func (t *blockingParallelTool) Exposure() tool.Exposure { return tool.ExposureDirect }
func (t *blockingParallelTool) Parallel() bool          { return true }
func (t *blockingParallelTool) Run(ctx context.Context, _ tool.Call) (tool.Result, error) {
	t.started <- struct{}{}
	select {
	case <-t.release:
		return tool.Result{Content: []tool.ContentPart{{Type: "text", Text: "done"}}}, nil
	case <-ctx.Done():
		return tool.Result{}, ctx.Err()
	}
}

func (t *blockingSequentialTool) Name() string            { return "sequential_test" }
func (t *blockingSequentialTool) Description() string     { return "test" }
func (t *blockingSequentialTool) Spec() []byte            { return []byte(`{"type":"object"}`) }
func (t *blockingSequentialTool) Exposure() tool.Exposure { return tool.ExposureDirect }
func (t *blockingSequentialTool) Run(ctx context.Context, call tool.Call) (tool.Result, error) {
	t.started <- call.ID
	select {
	case <-t.release:
		return tool.Result{Content: []tool.ContentPart{{Type: "text", Text: call.ID}}}, nil
	case <-ctx.Done():
		return tool.Result{}, ctx.Err()
	}
}

func TestParallelSafeToolCallsRunConcurrently(t *testing.T) {
	sessions := session.NewMemManager()
	sess, err := sessions.Create(session.CreateOptions{Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	log := state.NewMemLog()
	bus := broker.New[event.Event]()
	gateway := approval.NewGateway(bus, log)
	registry := tool.NewRegistry()
	parallel := &blockingParallelTool{
		started: make(chan struct{}, 2),
		release: make(chan struct{}, 2),
	}
	registry.Register(parallel)
	engine := NewEngine(log, bus, sessions, &parallelProvider{}, "test-model", registry, gateway)

	done := make(chan error, 1)
	go func() {
		done <- engine.RunTurn(context.Background(), sess.ID, "delegate")
	}()
	for i := 0; i < 2; i++ {
		select {
		case <-parallel.started:
		case <-time.After(time.Second):
			t.Fatal("parallel calls did not both start before release")
		}
	}
	parallel.release <- struct{}{}
	parallel.release <- struct{}{}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("turn did not complete")
	}

	history, err := log.History(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	var finalAssistant *message.Message
	for i := range history {
		if history[i].Role == message.RoleAssistant && len(history[i].ToolCalls) == 0 {
			finalAssistant = &history[i]
		}
	}
	if finalAssistant == nil ||
		finalAssistant.TurnStartedAt == nil ||
		finalAssistant.TurnCompletedAt == nil ||
		finalAssistant.TurnCompletedAt.Before(*finalAssistant.TurnStartedAt) {
		t.Fatalf("final assistant turn timestamps were not persisted: %#v", finalAssistant)
	}
}

func TestMixedToolBatchUsesParallelAndOrderedSequentialLanes(t *testing.T) {
	registry := tool.NewRegistry()
	parallel := &blockingParallelTool{
		started: make(chan struct{}, 1),
		release: make(chan struct{}, 1),
	}
	sequential := &blockingSequentialTool{
		started: make(chan string, 2),
		release: make(chan struct{}, 2),
	}
	registry.Register(parallel)
	registry.Register(sequential)
	engine := &Engine{
		tools: registry,
		log:   state.NewMemLog(),
		bus:   broker.New[event.Event](),
	}

	done := make(chan []executedToolCall, 1)
	go func() {
		done <- engine.executeToolCalls(context.Background(), "session-1", []message.ToolCall{
			{ID: "parallel-1", Name: "parallel_test", Input: []byte(`{}`)},
			{ID: "sequential-1", Name: "sequential_test", Input: []byte(`{}`)},
			{ID: "sequential-2", Name: "sequential_test", Input: []byte(`{}`)},
		}, &loopGuard{})
	}()

	select {
	case <-parallel.started:
	case <-time.After(time.Second):
		t.Fatal("parallel lane did not start")
	}
	select {
	case id := <-sequential.started:
		if id != "sequential-1" {
			t.Fatalf("first sequential call = %q", id)
		}
	case <-time.After(time.Second):
		t.Fatal("sequential lane did not start alongside parallel lane")
	}
	select {
	case id := <-sequential.started:
		t.Fatalf("second sequential call %q started before the first completed", id)
	case <-time.After(50 * time.Millisecond):
	}

	sequential.release <- struct{}{}
	select {
	case id := <-sequential.started:
		if id != "sequential-2" {
			t.Fatalf("second sequential call = %q", id)
		}
	case <-time.After(time.Second):
		t.Fatal("second sequential call did not start after the first completed")
	}
	sequential.release <- struct{}{}
	parallel.release <- struct{}{}

	select {
	case results := <-done:
		if len(results) != 3 ||
			results[0].call.ID != "parallel-1" ||
			results[1].call.ID != "sequential-1" ||
			results[2].call.ID != "sequential-2" {
			t.Fatalf("result order = %#v", results)
		}
	case <-time.After(time.Second):
		t.Fatal("mixed tool batch did not complete")
	}
}

func TestChildAgentUsesFrozenInstructionsAndRestrictedTools(t *testing.T) {
	sessions := session.NewMemManager()
	child, err := sessions.Create(session.CreateOptions{
		Model: "test-model", ParentID: "parent",
		AgentInstructions: "Only analyze market evidence.",
		AllowedTools:      []string{"read"},
	})
	if err != nil {
		t.Fatal(err)
	}
	log := state.NewMemLog()
	bus := broker.New[event.Event]()
	gateway := approval.NewGateway(bus, log)
	registry := tool.NewRegistry()
	registry.Register(staticTool{name: "read"})
	registry.Register(staticTool{name: "write"})
	prov := &captureProvider{}
	engine := NewEngine(log, bus, sessions, prov, "test-model", registry, gateway)

	if err := engine.RunTurn(context.Background(), child.ID, "research"); err != nil {
		t.Fatal(err)
	}
	if len(prov.request.Messages) == 0 ||
		!strings.Contains(prov.request.Messages[0].Parts[0].Text, "Only analyze market evidence.") {
		t.Fatalf("system prompt does not contain agent instructions: %#v", prov.request.Messages)
	}
	if len(prov.request.Tools) != 1 || prov.request.Tools[0].Function.Name != "read" {
		t.Fatalf("child tools = %#v", prov.request.Tools)
	}
	if engine.approvalEventSession(child.ID) != "parent" {
		t.Fatalf("approval event session = %q, want parent", engine.approvalEventSession(child.ID))
	}
}
