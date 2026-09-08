package kernel

import (
	"context"
	"strings"
	"testing"
	"time"

	conversation "github.com/freesoulcode/foya/internal/conversation"
	workflow "github.com/freesoulcode/foya/internal/workflow"
)

func TestApproveWorkflowImmediatelyStartsExecution(t *testing.T) {
	be, sessionID, provider := newQueueTestBackend(t)
	manager, err := workflow.NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	be.SetWorkflowManager(manager)
	record, err := manager.Start(sessionID, workflow.KindPlan, "实现功能", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	record, err = manager.Update(record.ID, workflow.StatusReady, "## Plan\n1. 修改代码\n2. 运行测试")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := be.sessions.SetAgentMode(
		sessionID,
		conversation.AgentModePlanReady,
		conversation.AgentModeExecute,
	); err != nil {
		t.Fatal(err)
	}

	result, err := be.ApproveWorkflow(context.Background(), sessionID, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Workflow.Status != workflow.StatusApproved ||
		result.Submission.Status != SubmissionStarted {
		t.Fatalf("approval result = %#v", result)
	}
	select {
	case prompt := <-provider.started:
		if !strings.Contains(prompt, "Execute the approved plan at "+record.Path) {
			t.Fatalf("execution prompt = %q", prompt)
		}
	case <-time.After(time.Second):
		t.Fatal("approved plan did not start automatically")
	}
	provider.releases <- struct{}{}
}

func TestClosePlanWorkflowRestoresPreviousMode(t *testing.T) {
	be, sessionID, _ := newQueueTestBackend(t)
	manager, err := workflow.NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	be.SetWorkflowManager(manager)
	record, err := manager.Start(sessionID, workflow.KindPlan, "检查设计", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := be.sessions.SetAgentMode(
		sessionID,
		conversation.AgentModePlan,
		conversation.AgentModeExecute,
	); err != nil {
		t.Fatal(err)
	}

	closed, err := be.CloseWorkflow(context.Background(), sessionID, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if closed.Status != workflow.StatusClosed {
		t.Fatalf("closed workflow = %#v", closed)
	}
	current, ok := be.sessions.Get(sessionID)
	if !ok || current.AgentMode != conversation.AgentModeExecute || current.PrePlanMode != "" {
		t.Fatalf("restored session = %#v, %v", current, ok)
	}
}

func TestApproveSpecInitializesTasksAndStartsExecution(t *testing.T) {
	be, sessionID, provider := newQueueTestBackend(t)
	manager, err := workflow.NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	be.SetWorkflowManager(manager)
	record, err := manager.Start(sessionID, workflow.KindSpec, "实现账户恢复", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	record, err = manager.SubmitSpec(sessionID, workflow.SpecDocuments{
		Title:     "Account Recovery",
		Spec:      "# Specification\n\n实现账户恢复。",
		Tasks:     "# Tasks\n\n- [ ] T001 添加接口\n- [ ] T002 添加测试",
		Checklist: "# Checklist\n\n- [ ] 正常流程通过\n- [ ] 错误流程通过",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := be.CompleteWorkflow(sessionID, "Spec documents are ready."); err != nil {
		t.Fatal(err)
	}
	record, ok := manager.Get(record.ID)
	if !ok || record.Status != workflow.StatusReady {
		t.Fatalf("completed spec = %#v, %v", record, ok)
	}

	result, err := be.ApproveWorkflow(context.Background(), sessionID, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Workflow.Status != workflow.StatusApproved ||
		result.Submission.Status != SubmissionStarted {
		t.Fatalf("approval result = %#v", result)
	}
	current, ok := be.sessions.Get(sessionID)
	if !ok || len(current.Tasks) != 2 ||
		current.Tasks[0].Content != "T001 添加接口" ||
		current.Tasks[0].Status != conversation.TaskStatusPending {
		t.Fatalf("session tasks = %#v", current)
	}
	select {
	case prompt := <-provider.started:
		if !strings.Contains(prompt, "Implement the approved specification now.") ||
			!strings.Contains(prompt, record.Artifacts.Tasks) ||
			!strings.Contains(prompt, record.Artifacts.Checklist) {
			t.Fatalf("execution prompt = %q", prompt)
		}
	case <-time.After(time.Second):
		t.Fatal("approved spec did not start automatically")
	}
	provider.releases <- struct{}{}
}

func TestSpecCommandRequiresProject(t *testing.T) {
	be, sessionID, _ := newQueueTestBackend(t)
	be.SetCommandManager(workflow.NewCommandManager(t.TempDir()))
	manager, err := workflow.NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	be.SetWorkflowManager(manager)

	items, err := be.SessionCommands(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Ref == "builtin:spec" {
			t.Fatal("spec command should be hidden without a project")
		}
	}
	if _, err := be.ExecuteCommand(
		context.Background(),
		sessionID,
		"spec",
		"Define account recovery",
	); err == nil || !strings.Contains(err.Error(), "requires a project") {
		t.Fatalf("execute spec error = %v", err)
	}
}
