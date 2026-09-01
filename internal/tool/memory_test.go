package tool

import (
	"context"
	"testing"

	"github.com/freesoulcode/foya/internal/contextdata"
)

func TestMemoryRememberUsesCurrentProjectByDefault(t *testing.T) {
	store, err := contextdata.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tool := NewMemoryRememberTool(store)
	ctx := WithProjectID(context.Background(), "project-1")
	result, err := tool.Run(ctx, Call{Input: []byte(`{"content":"Use REST and SSE."}`)})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("result = %#v", result)
	}
	items := store.ListMemories(contextdata.ScopeProject, "project-1")
	if len(items) != 1 || items[0].Content != "Use REST and SSE." {
		t.Fatalf("items = %#v", items)
	}
}

func TestMemoryRememberRejectsProjectScopeWithoutProject(t *testing.T) {
	store, err := contextdata.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	result, err := NewMemoryRememberTool(store).Run(
		context.Background(),
		Call{Input: []byte(`{"content":"Use REST.","scope":"project"}`)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("result = %#v", result)
	}
}

func TestMemoryRememberRejectsWritesWhenDisabled(t *testing.T) {
	store, err := contextdata.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateMemorySettings(contextdata.MemorySettings{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	result, err := NewMemoryRememberTool(store).Run(
		context.Background(),
		Call{Input: []byte(`{"content":"Use REST."}`)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("result = %#v", result)
	}
}
