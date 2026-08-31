package session

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestPersistentManagerRestoresChildRuntimeSnapshot(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	manager, err := NewPersistentManager(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	created, err := manager.Create(CreateOptions{
		Model: "model", ParentID: "parent", AgentRef: "user:researcher",
		AgentInstructions: "research carefully", AllowedTools: []string{"read"},
		AgentMaxTurns: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	restored, err := NewPersistentManager(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	item, ok := restored.Get(created.ID)
	if !ok {
		t.Fatal("restored session missing")
	}
	if item.AgentInstructions != "research carefully" ||
		len(item.AllowedTools) != 1 || item.AgentMaxTurns != 7 {
		t.Fatalf("runtime snapshot = %+v", item)
	}
}

func stringPointer(value string) *string {
	return &value
}

func TestUpdateLocksBoundProject(t *testing.T) {
	manager := NewMemManager()
	created, err := manager.Create(CreateOptions{
		Model:     "model-a",
		ProjectID: "project-alpha",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = manager.Update(
		created.ID,
		nil,
		stringPointer("model-b"),
		nil,
		stringPointer("project-beta"),
		nil,
	)
	if !errors.Is(err, ErrProjectLocked) {
		t.Fatalf("Update project error = %v, want %v", err, ErrProjectLocked)
	}

	current, ok := manager.Get(created.ID)
	if !ok {
		t.Fatal("session disappeared after rejected update")
	}
	if current.Model != "model-a" || current.ProjectID != "project-alpha" {
		t.Fatalf("rejected update mutated session: %+v", current)
	}

	updated, err := manager.Update(
		created.ID,
		nil,
		stringPointer("model-b"),
		nil,
		stringPointer("project-alpha"),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Model != "model-b" {
		t.Fatalf("Model = %q, want model-b", updated.Model)
	}
}

func TestUpdateAllowsFirstProjectBinding(t *testing.T) {
	manager := NewMemManager()
	created, err := manager.Create(CreateOptions{Model: "model-a"})
	if err != nil {
		t.Fatal(err)
	}

	updated, err := manager.Update(
		created.ID,
		nil,
		nil,
		nil,
		stringPointer("project-alpha"),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ProjectID != "project-alpha" {
		t.Fatalf("ProjectID = %q, want project-alpha", updated.ProjectID)
	}

	_, err = manager.Update(created.ID, nil, nil, nil, stringPointer(""), nil)
	if !errors.Is(err, ErrProjectLocked) {
		t.Fatalf("Clear project error = %v, want %v", err, ErrProjectLocked)
	}
}

func TestUpdateReasoningEffort(t *testing.T) {
	manager := NewMemManager()
	created, err := manager.Create(CreateOptions{Model: "model-a"})
	if err != nil {
		t.Fatal(err)
	}

	updated, err := manager.Update(
		created.ID,
		nil,
		nil,
		stringPointer(string(ReasoningEffortHigh)),
		nil,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ReasoningEffort != ReasoningEffortHigh {
		t.Fatalf("ReasoningEffort = %q, want %q", updated.ReasoningEffort, ReasoningEffortHigh)
	}

	_, err = manager.Update(created.ID, nil, nil, stringPointer("maximum"), nil, nil)
	if !errors.Is(err, ErrInvalidReasoningEffort) {
		t.Fatalf("invalid effort error = %v, want %v", err, ErrInvalidReasoningEffort)
	}
}

func TestCreatePreservesChildAgentSnapshot(t *testing.T) {
	manager := NewMemManager()
	parent, err := manager.Create(CreateOptions{Model: "model-a"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := manager.Create(CreateOptions{
		ParentID:          parent.ID,
		SpawnedBy:         &SpawnedBy{ParentToolCallID: "call-1"},
		AgentRef:          "project:p1:researcher",
		AgentName:         "researcher",
		AgentDigest:       "digest",
		AgentInstructions: "instructions",
		AllowedTools:      []string{"read"},
		AgentMaxTurns:     5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if child.ParentID != parent.ID || child.SpawnedBy == nil ||
		child.SpawnedBy.ParentToolCallID != "call-1" {
		t.Fatalf("lineage = %+v", child)
	}
	if child.AgentInstructions != "instructions" || child.AgentMaxTurns != 5 ||
		len(child.AllowedTools) != 1 || child.AllowedTools[0] != "read" {
		t.Fatalf("runtime snapshot = %+v", child)
	}
}
