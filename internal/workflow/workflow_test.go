package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freesoulcode/foya/internal/session"
	"github.com/freesoulcode/foya/internal/tool"
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

func TestCloseWorkflowEndsReadOnlyPolicy(t *testing.T) {
	manager, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	record, err := manager.Start("session-1", KindPlan, "梳理实现", "")
	if err != nil {
		t.Fatal(err)
	}
	closed, err := manager.Close("session-1", record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if closed.Status != StatusClosed || closed.Revision != 2 {
		t.Fatalf("closed workflow = %#v", closed)
	}
	if _, ok := manager.Active("session-1"); ok {
		t.Fatal("closed workflow remains active")
	}
	if _, ok := manager.Policy("session-1"); ok {
		t.Fatal("closed workflow keeps read-only policy")
	}
	if _, err := manager.Close("session-1", record.ID); !errors.Is(err, ErrInvalidStatus) {
		t.Fatalf("close closed workflow error = %v", err)
	}
	if _, err := manager.Close("other-session", record.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("close workflow from another session error = %v", err)
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

func TestSpecWorkflowCreatesThreeReviewableArtifacts(t *testing.T) {
	projectPath := t.TempDir()
	manager, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	started, err := manager.Start("session-1", KindSpec, "Add account recovery", projectPath)
	if err != nil {
		t.Fatal(err)
	}
	if started.Artifacts != nil || started.Path != "" {
		t.Fatalf("spec artifacts should wait for generated title: %#v", started)
	}
	if _, completed, err := manager.CompleteActive(
		"session-1",
		"Spec documents are ready.",
	); !errors.Is(err, ErrSpecIncomplete) || completed {
		t.Fatalf("incomplete spec completion = %v, %v", completed, err)
	}

	documents := SpecDocuments{
		Title:     "Account Recovery",
		Spec:      "# Specification\n\n## Requirements\n\n- Account recovery",
		Tasks:     "# Tasks\n\n- [ ] T001 Add recovery endpoint\n- [ ] T002 Add tests",
		Checklist: "# Acceptance Checklist\n\n- [ ] Recovery succeeds\n- [ ] Invalid token fails",
	}
	ready, err := manager.SubmitSpec("session-1", documents)
	if err != nil {
		t.Fatal(err)
	}
	if ready.Status != StatusReady || ready.Content != documents.Spec {
		t.Fatalf("ready spec = %#v", ready)
	}
	wantDir := filepath.Join(projectPath, ".foya", "specs", "account-recovery")
	if ready.Title != documents.Title ||
		filepath.Dir(ready.Artifacts.Spec) != wantDir ||
		ready.Artifacts.Tasks != filepath.Join(wantDir, "tasks.md") ||
		ready.Artifacts.Checklist != filepath.Join(wantDir, "checklist.md") {
		t.Fatalf("spec artifacts = %#v, want directory %q", ready.Artifacts, wantDir)
	}
	for path, want := range map[string]string{
		ready.Artifacts.Spec:      documents.Spec,
		ready.Artifacts.Tasks:     documents.Tasks,
		ready.Artifacts.Checklist: documents.Checklist,
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(string(data)) != want {
			t.Fatalf("%s = %q, want %q", path, data, want)
		}
	}
	completed, ok, err := manager.CompleteActive("session-1", "Spec documents are ready.")
	if err != nil || !ok || completed.Status != StatusReady {
		t.Fatalf("complete spec = %#v, %v, %v", completed, ok, err)
	}
}

func TestSpecWorkflowUsesReadOnlyPolicyAndSubmitTool(t *testing.T) {
	manager, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Start("session-1", KindSpec, "Define exports", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	policy, ok := manager.Policy("session-1")
	if !ok || !strings.Contains(policy.Instructions, "spec.md") ||
		!strings.Contains(policy.Instructions, SubmitSpecToolName) {
		t.Fatalf("spec policy = %#v, %v", policy, ok)
	}
	for _, forbidden := range []string{"write", "edit", "bash"} {
		for _, allowed := range policy.AllowedTools {
			if allowed == forbidden {
				t.Fatalf("spec policy unexpectedly allows %q", forbidden)
			}
		}
	}
	if len(policy.ExtraTools) != 1 || policy.ExtraTools[0] != SubmitSpecToolName {
		t.Fatalf("spec extra tools = %#v", policy.ExtraTools)
	}
}

func TestSubmitSpecToolAndTaskProgress(t *testing.T) {
	manager, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	started, err := manager.Start("session-1", KindSpec, "Track work", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(SpecDocuments{
		Title:     "Track Work",
		Spec:      "# Specification\n\nTrack work.",
		Tasks:     "# Tasks\n\n- [ ] T001 First task\n- [ ] T002 Second task",
		Checklist: "# Checklist\n\n- [ ] Tests pass",
	})
	result, err := NewSubmitSpecTool(manager).Run(
		tool.WithSessionID(context.Background(), "session-1"),
		tool.Call{Input: payload},
	)
	if err != nil || result.IsError {
		t.Fatalf("submit spec result = %#v, %v", result, err)
	}
	items, err := manager.SpecTaskItems(started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 ||
		items[0].Content != "T001 First task" ||
		items[0].Status != session.TaskStatusPending {
		t.Fatalf("spec task items = %#v", items)
	}
	record, changed, err := manager.SyncSpecTaskProgress("session-1", []session.Task{{
		Content: "T001 First task",
		Status:  session.TaskStatusCompleted,
	}, {
		Content: "T002 Second task",
		Status:  session.TaskStatusInProgress,
	}})
	if err != nil || changed {
		t.Fatalf("unapproved spec progress = %#v, %v, %v", record, changed, err)
	}
	if _, err := manager.Approve(started.ID); err != nil {
		t.Fatal(err)
	}
	record, changed, err = manager.SyncSpecTaskProgress("session-1", []session.Task{{
		Content: "T001 First task",
		Status:  session.TaskStatusCompleted,
	}, {
		Content: "T002 Second task",
		Status:  session.TaskStatusInProgress,
	}})
	if err != nil || !changed {
		t.Fatalf("approved spec progress = %#v, %v, %v", record, changed, err)
	}
	data, err := os.ReadFile(record.Artifacts.Tasks)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "- [x] T001 First task") ||
		!strings.Contains(string(data), "- [ ] T002 Second task") {
		t.Fatalf("synchronized tasks = %q", data)
	}
	record, changed, err = manager.SyncSpecTaskProgress("session-1", []session.Task{{
		Content: "T001 First task",
		Status:  session.TaskStatusCompleted,
	}, {
		Content: "T002 Second task",
		Status:  session.TaskStatusCompleted,
	}})
	if err != nil || !changed || record.Status != StatusCompleted {
		t.Fatalf("completed spec progress = %#v, %v, %v", record, changed, err)
	}
}
