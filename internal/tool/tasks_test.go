package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	conversation "github.com/freesoulcode/foya/internal/conversation"
)

func TestReadTasksToolReturnsCurrentTasks(t *testing.T) {
	manager := newTestSessionManager(t)
	created, err := manager.Create(conversation.CreateOptions{Model: "model"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.SetTasks(created.ID, []conversation.Task{
		{Content: "Read code", Status: conversation.TaskStatusCompleted},
		{Content: "Write tests", Status: conversation.TaskStatusInProgress},
	}); err != nil {
		t.Fatal(err)
	}
	tool := NewReadTasksTool(manager)
	result, err := tool.Run(WithSessionID(context.Background(), created.ID), Call{Input: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %#v", result)
	}
	var response readTasksResponse
	if err := json.Unmarshal([]byte(result.Content[0].Text), &response); err != nil {
		t.Fatal(err)
	}
	if response.Total != 2 || response.Completed != 1 ||
		response.InProgress != "Write tests" ||
		len(response.Tasks) != 2 {
		t.Fatalf("response = %#v", response)
	}
}

func TestReadTasksToolReturnsEmptyList(t *testing.T) {
	manager := newTestSessionManager(t)
	created, err := manager.Create(conversation.CreateOptions{Model: "model"})
	if err != nil {
		t.Fatal(err)
	}
	tool := NewReadTasksTool(manager)
	result, err := tool.Run(WithSessionID(context.Background(), created.ID), Call{Input: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %#v", result)
	}
	if !strings.Contains(result.Content[0].Text, `"tasks":[]`) ||
		!strings.Contains(result.Content[0].Text, `"total":0`) {
		t.Fatalf("output = %s", result.Content[0].Text)
	}
}

func TestReadTasksToolRequiresSessionID(t *testing.T) {
	tool := NewReadTasksTool(newTestSessionManager(t))
	result, err := tool.Run(context.Background(), Call{Input: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(result.Content[0].Text, "session id is required") {
		t.Fatalf("result = %#v", result)
	}
}

func TestUpdateTasksToolPersistsAndSummarizes(t *testing.T) {
	manager := newTestSessionManager(t)
	created, err := manager.Create(conversation.CreateOptions{Model: "model"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.SetTasks(created.ID, []conversation.Task{
		{Content: "Read code", Status: conversation.TaskStatusInProgress},
	}); err != nil {
		t.Fatal(err)
	}
	notifications := 0
	tool := NewUpdateTasksTool(manager, func(context.Context, *conversation.Session) {
		notifications++
	})
	result, err := tool.Run(
		WithSessionID(context.Background(), created.ID),
		Call{Input: []byte(`{"tasks":[{"content":"Read code","status":"completed"},{"content":"Write tests","status":"in_progress"}]}`)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %#v", result)
	}
	if notifications != 1 {
		t.Fatalf("notifications = %d, want 1", notifications)
	}
	updated, ok := manager.Get(created.ID)
	if !ok {
		t.Fatal("session missing")
	}
	if len(updated.Tasks) != 2 || updated.Tasks[0].Status != conversation.TaskStatusCompleted {
		t.Fatalf("tasks = %#v", updated.Tasks)
	}
	output := result.Content[0].Text
	if !strings.Contains(output, `"completed":1`) ||
		!strings.Contains(output, `"in_progress":"Write tests"`) ||
		!strings.Contains(output, `"just_completed":["Read code"]`) {
		t.Fatalf("output = %s", output)
	}
}

func TestUpdateTasksToolRejectsMultipleInProgress(t *testing.T) {
	manager := newTestSessionManager(t)
	created, err := manager.Create(conversation.CreateOptions{Model: "model"})
	if err != nil {
		t.Fatal(err)
	}
	tool := NewUpdateTasksTool(manager, nil)
	result, err := tool.Run(
		WithSessionID(context.Background(), created.ID),
		Call{Input: []byte(`{"tasks":[{"content":"One","status":"in_progress"},{"content":"Two","status":"in_progress"}]}`)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(result.Content[0].Text, "at most one task") {
		t.Fatalf("result = %#v", result)
	}
}

func TestUpdateTasksToolRejectsInvalidStatus(t *testing.T) {
	manager := newTestSessionManager(t)
	created, err := manager.Create(conversation.CreateOptions{Model: "model"})
	if err != nil {
		t.Fatal(err)
	}
	tool := NewUpdateTasksTool(manager, nil)
	result, err := tool.Run(
		WithSessionID(context.Background(), created.ID),
		Call{Input: []byte(`{"tasks":[{"content":"One","status":"blocked"}]}`)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(result.Content[0].Text, "invalid status") {
		t.Fatalf("result = %#v", result)
	}
}

func TestUpdateTasksToolRequiresSessionID(t *testing.T) {
	tool := NewUpdateTasksTool(newTestSessionManager(t), nil)
	result, err := tool.Run(context.Background(), Call{Input: []byte(`{"tasks":[]}`)})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || !strings.Contains(result.Content[0].Text, "session id is required") {
		t.Fatalf("result = %#v", result)
	}
}
