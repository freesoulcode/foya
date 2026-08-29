package session

import (
	"errors"
	"testing"
)

func stringPointer(value string) *string {
	return &value
}

func TestUpdateLocksBoundWorkspace(t *testing.T) {
	manager := NewMemManager()
	created, err := manager.Create(CreateOptions{
		Model:     "model-a",
		Workspace: "/projects/alpha",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = manager.Update(
		created.ID,
		stringPointer("model-b"),
		stringPointer("/projects/beta"),
		nil,
	)
	if !errors.Is(err, ErrWorkspaceLocked) {
		t.Fatalf("Update workspace error = %v, want %v", err, ErrWorkspaceLocked)
	}

	current, ok := manager.Get(created.ID)
	if !ok {
		t.Fatal("session disappeared after rejected update")
	}
	if current.Model != "model-a" || current.Workspace != "/projects/alpha" {
		t.Fatalf("rejected update mutated session: %+v", current)
	}

	updated, err := manager.Update(
		created.ID,
		stringPointer("model-b"),
		stringPointer("/projects/alpha"),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Model != "model-b" {
		t.Fatalf("Model = %q, want model-b", updated.Model)
	}
}

func TestUpdateAllowsFirstWorkspaceBinding(t *testing.T) {
	manager := NewMemManager()
	created, err := manager.Create(CreateOptions{Model: "model-a"})
	if err != nil {
		t.Fatal(err)
	}

	updated, err := manager.Update(
		created.ID,
		nil,
		stringPointer("/projects/alpha"),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Workspace != "/projects/alpha" {
		t.Fatalf("Workspace = %q, want /projects/alpha", updated.Workspace)
	}

	_, err = manager.Update(created.ID, nil, stringPointer(""), nil)
	if !errors.Is(err, ErrWorkspaceLocked) {
		t.Fatalf("Clear workspace error = %v, want %v", err, ErrWorkspaceLocked)
	}
}
