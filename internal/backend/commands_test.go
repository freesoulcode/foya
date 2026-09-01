package backend

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/freesoulcode/foya/internal/session"
	"github.com/freesoulcode/foya/internal/workflow"
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
		session.AgentModePlanReady,
		session.AgentModeExecute,
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
