package conversation

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/freesoulcode/foya/internal/storage"
)

func newTestManager(t testing.TB) Manager {
	t.Helper()
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	})
	manager, err := NewManager(db)
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func TestManagerPersistsSessionState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "foya.db")
	db, err := storage.OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(db)
	if err != nil {
		t.Fatal(err)
	}
	created, err := manager.Create(CreateOptions{
		ParentID:          "parent",
		SpawnedBy:         &SpawnedBy{ParentToolCallID: "call-1"},
		AgentRef:          "project:p1:researcher",
		AgentName:         "researcher",
		AgentDigest:       "digest",
		ConnectionID:      "connection-1",
		Model:             "model-a",
		ReasoningEffort:   ReasoningEffortHigh,
		ProjectID:         "project-1",
		ApprovalMode:      "manual",
		AgentInstructions: "research carefully",
		AllowedTools:      []string{"read", "web_fetch"},
		AgentMaxTurns:     8,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.SetTasks(created.ID, []Task{{
		Content: "inspect source", Status: TaskStatusInProgress,
	}}); err != nil {
		t.Fatal(err)
	}
	if changed, err := manager.SetGeneratedTitle(created.ID, "Generated"); err != nil || !changed {
		t.Fatalf("set generated title: changed=%v err=%v", changed, err)
	}
	if _, err := manager.SetPinned(created.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.SetAgentMode(
		created.ID, AgentModePlanReady, AgentModePlan,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.SetPhase(created.ID, PhaseTurn); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = storage.OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	manager, err = NewManager(db)
	if err != nil {
		t.Fatal(err)
	}
	restored, ok := manager.Get(created.ID)
	if !ok {
		t.Fatal("restored session missing")
	}
	if restored.Phase != PhaseIdle {
		t.Fatalf("Phase = %q, want idle", restored.Phase)
	}
	if restored.AgentMode != AgentModePlanReady ||
		restored.PrePlanMode != AgentModePlan {
		t.Fatalf("agent modes = %q/%q", restored.AgentMode, restored.PrePlanMode)
	}
	if restored.SpawnedBy == nil ||
		restored.SpawnedBy.ParentToolCallID != "call-1" {
		t.Fatalf("SpawnedBy = %#v", restored.SpawnedBy)
	}
	if restored.AgentInstructions != "research carefully" ||
		len(restored.AllowedTools) != 2 || restored.AgentMaxTurns != 8 {
		t.Fatalf("runtime snapshot = %#v", restored)
	}
	if len(restored.Tasks) != 1 ||
		restored.Tasks[0].Status != TaskStatusInProgress {
		t.Fatalf("Tasks = %#v", restored.Tasks)
	}
	if restored.Title != "Generated" || !restored.Pinned ||
		restored.PinnedAt == nil {
		t.Fatalf("presentation state = %#v", restored)
	}
}

func TestManagerPreservesValidationAndTitleSemantics(t *testing.T) {
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	manager, err := NewManager(db)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := manager.Create(CreateOptions{
		ReasoningEffort: "maximum",
	}); !errors.Is(err, ErrInvalidReasoningEffort) {
		t.Fatalf("invalid reasoning error = %v", err)
	}
	created, err := manager.Create(CreateOptions{Model: "model-a"})
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := manager.SetGeneratedTitle(created.ID, "Generated"); err != nil || !changed {
		t.Fatalf("first generated title: changed=%v err=%v", changed, err)
	}
	if changed, err := manager.SetGeneratedTitle(created.ID, "Replacement"); err != nil || changed {
		t.Fatalf("second generated title: changed=%v err=%v", changed, err)
	}
	if err := manager.Rename(created.ID, "Manual"); err != nil {
		t.Fatal(err)
	}
	if item, changed, err := manager.ResetGeneratedTitle(created.ID); err != nil ||
		changed || item.Title != "Manual" {
		t.Fatalf("reset manual title: item=%#v changed=%v err=%v", item, changed, err)
	}

	project := "project-a"
	if _, err := manager.Update(
		created.ID, nil, nil, nil, &project, nil,
	); err != nil {
		t.Fatal(err)
	}
	otherProject := "project-b"
	if _, err := manager.Update(
		created.ID, nil, nil, nil, &otherProject, nil,
	); !errors.Is(err, ErrProjectLocked) {
		t.Fatalf("project lock error = %v", err)
	}
}

func TestManagerDeleteIsDurable(t *testing.T) {
	db, err := storage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	manager, err := NewManager(db)
	if err != nil {
		t.Fatal(err)
	}
	created, err := manager.Create(CreateOptions{Model: "model-a"})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Delete(created.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.Get(created.ID); ok {
		t.Fatal("deleted session is still readable")
	}
	if err := manager.Delete(created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete error = %v", err)
	}
}
