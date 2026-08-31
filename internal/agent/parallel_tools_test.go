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
		!strings.Contains(prov.request.Messages[0].Content, "Only analyze market evidence.") {
		t.Fatalf("system prompt does not contain agent instructions: %#v", prov.request.Messages)
	}
	if len(prov.request.Tools) != 1 || prov.request.Tools[0].Function.Name != "read" {
		t.Fatalf("child tools = %#v", prov.request.Tools)
	}
	if engine.approvalEventSession(child.ID) != "parent" {
		t.Fatalf("approval event session = %q, want parent", engine.approvalEventSession(child.ID))
	}
}
