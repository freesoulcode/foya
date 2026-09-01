package agent

import (
	"context"
	"testing"
	"time"

	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/tool"
)

type cancellableTool struct {
	started chan struct{}
}

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
