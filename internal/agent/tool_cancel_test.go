package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/tool"
)

type cancellableTool struct {
	started chan struct{}
}

type deferredAgentTestTool struct{}

func (t *cancellableTool) Name() string            { return "cancellable" }
func (t *cancellableTool) Description() string     { return "test" }
func (t *cancellableTool) Spec() []byte            { return []byte(`{"type":"object"}`) }
func (t *cancellableTool) Exposure() tool.Exposure { return tool.ExposureDirect }
func (t *cancellableTool) Run(ctx context.Context, _ tool.Call) (tool.Result, error) {
	close(t.started)
	<-ctx.Done()
	return tool.Result{
		IsError: true,
		Content: []tool.ContentPart{{Type: "text", Text: "cancelled"}},
	}, nil
}

func TestCancelToolStopsOnlyToolContext(t *testing.T) {
	registry := tool.NewRegistry()
	blocking := &cancellableTool{started: make(chan struct{})}
	registry.Register(blocking)
	engine := &Engine{tools: registry}
	parentCtx, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()
	done := make(chan tool.Result, 1)
	go func() {
		done <- engine.executeTool(parentCtx, "session-1", message.ToolCall{
			ID: "call-1", Name: "cancellable", Input: []byte(`{}`),
		})
	}()
	<-blocking.started
	if !engine.CancelTool("session-1", "call-1") {
		t.Fatal("running tool was not found")
	}
	select {
	case result := <-done:
		if !result.IsError {
			t.Fatalf("cancelled result = %#v", result)
		}
		if got := result.Content[0].Text; got != toolCancelledByUserResult {
			t.Fatalf("cancelled result text = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("tool did not stop")
	}
	if parentCtx.Err() != nil {
		t.Fatalf("tool cancellation propagated to turn: %v", parentCtx.Err())
	}
}

func TestExecuteToolRejectsDeferredToolNotActiveForStep(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(deferredAgentTestTool{})
	engine := &Engine{tools: registry}

	result := engine.executeTool(
		tool.WithActiveToolSnapshot(context.Background(), map[string]bool{}),
		"session-1",
		message.ToolCall{ID: "call-1", Name: "deferred_test", Input: []byte(`{}`)},
	)
	if !result.IsError || !strings.Contains(result.Content[0].Text, "has not been activated") {
		t.Fatalf("inactive deferred result = %#v", result)
	}

	result = engine.executeTool(
		tool.WithActiveToolSnapshot(
			context.Background(),
			map[string]bool{"deferred_test": true},
		),
		"session-1",
		message.ToolCall{ID: "call-2", Name: "deferred_test", Input: []byte(`{}`)},
	)
	if result.IsError || result.Content[0].Text != "ok" {
		t.Fatalf("active deferred result = %#v", result)
	}
}

func (t deferredAgentTestTool) Name() string            { return "deferred_test" }
func (t deferredAgentTestTool) Description() string     { return "test" }
func (t deferredAgentTestTool) Spec() []byte            { return []byte(`{"type":"object"}`) }
func (t deferredAgentTestTool) Exposure() tool.Exposure { return tool.ExposureDeferred }
func (t deferredAgentTestTool) Run(context.Context, tool.Call) (tool.Result, error) {
	return tool.Result{Content: []tool.ContentPart{{Type: "text", Text: "ok"}}}, nil
}
