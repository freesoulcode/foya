package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStartPersistsAndReplacesActiveWorkflow(t *testing.T) {
	dataDir := t.TempDir()
	manager, err := NewManager(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := manager.Start("session-1", KindPlan, "设计命令工作流", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Start("session-1", KindSpec, "定义接口", ""); err != nil {
		t.Fatal(err)
	}
	if current, ok := manager.Active("session-1"); !ok || current.Kind != KindSpec {
		t.Fatalf("active workflow = %#v, %v", current, ok)
	}
	if prior, ok := manager.Get(plan.ID); !ok || prior.Status != StatusClosed {
		t.Fatalf("prior workflow = %#v, %v", prior, ok)
	}
	reloaded, err := NewManager(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if current, ok := reloaded.Active("session-1"); !ok || current.Goal != "定义接口" {
		t.Fatalf("reloaded workflow = %#v, %v", current, ok)
	}
}

func TestWorkflowPolicyAndUpdate(t *testing.T) {
	manager, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	record, err := manager.Start("session-1", KindPlan, "梳理实现", "")
	if err != nil {
		t.Fatal(err)
	}
	policy, ok := manager.Policy("session-1")
	if !ok || len(policy.AllowedTools) == 0 {
		t.Fatalf("policy = %#v, %v", policy, ok)
	}
	updated, err := manager.Update(record.ID, StatusReady, "1. 探索\n2. 实现")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != StatusReady || updated.Revision != 2 {
		t.Fatalf("updated = %#v", updated)
	}
	if policy, ok := manager.Policy("session-1"); !ok || len(policy.AllowedTools) == 0 {
		t.Fatalf("ready workflow must remain read-only until approval: %#v, %v", policy, ok)
	}
}

func TestCompleteActiveStoresFinalResponse(t *testing.T) {
	dataDir := t.TempDir()
	projectPath := filepath.Join(t.TempDir(), "project")
	manager, err := NewManager(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	started, err := manager.Start("session-1", KindPlan, "规划重构", projectPath)
	if err != nil {
		t.Fatal(err)
	}
	wantRoot := filepath.Join(projectPath, ".foya", "plans")
	if filepath.Dir(started.Path) != wantRoot {
		t.Fatalf("plan path = %q, want root %q", started.Path, wantRoot)
	}
	record, completed, err := manager.CompleteActive("session-1", "## Plan\n1. 读取代码\n2. 修改实现")
	if err != nil {
		t.Fatal(err)
	}
	if !completed || record.Status != StatusReady || record.Content == "" {
		t.Fatalf("completion = %#v, %v", record, completed)
	}
	if policy, ok := manager.Policy("session-1"); !ok || len(policy.AllowedTools) == 0 {
		t.Fatalf("ready workflow should remain constrained: %#v, %v", policy, ok)
	}
	data, err := os.ReadFile(record.Path)
	if err != nil {
		t.Fatalf("read plan artifact: %v", err)
	}
	if !strings.Contains(string(data), "status: ready") ||
		!strings.Contains(string(data), "## Plan") {
		t.Fatalf("ready plan artifact = %q", data)
	}
	approved, err := manager.Approve(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(approved.Path)
	if err != nil {
		t.Fatalf("read approved plan artifact: %v", err)
	}
	if !strings.Contains(string(data), "status: approved") {
		t.Fatalf("approved plan artifact = %q", data)
	}
}
