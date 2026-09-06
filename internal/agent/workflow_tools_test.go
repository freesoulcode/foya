package agent

import (
	"context"
	"testing"

	"github.com/freesoulcode/foya/internal/tool"
	"github.com/freesoulcode/foya/internal/workflow"
)

type hiddenWorkflowTool struct{}

func (hiddenWorkflowTool) Name() string               { return workflow.SubmitSpecToolName }
func (hiddenWorkflowTool) Description() string        { return "submit spec" }
func (hiddenWorkflowTool) Spec() []byte                { return []byte(`{"type":"object"}`) }
func (hiddenWorkflowTool) Exposure() tool.Exposure     { return tool.ExposureHidden }
func (hiddenWorkflowTool) Run(context.Context, tool.Call) (tool.Result, error) {
	return tool.Result{}, nil
}

func TestWorkflowPolicyActivatesHiddenTool(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(hiddenWorkflowTool{})
	engine := &Engine{tools: registry}

	if defs := engine.toolDefsForSession("session-1", nil); len(defs) != 0 {
		t.Fatalf("hidden tool leaked without workflow policy: %#v", defs)
	}
	engine.SetWorkflowPolicyResolver(func(string) (workflow.Policy, bool) {
		return workflow.Policy{
			AllowedTools: []string{workflow.SubmitSpecToolName},
			ExtraTools:   []string{workflow.SubmitSpecToolName},
		}, true
	})
	defs := engine.toolDefsForSession("session-1", nil)
	if len(defs) != 1 || defs[0].Function.Name != workflow.SubmitSpecToolName {
		t.Fatalf("workflow tool defs = %#v", defs)
	}
}
