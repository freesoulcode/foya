package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	conversation "github.com/freesoulcode/foya/internal/conversation"
)

const (
	maxSessionTasks        = 100
	maxSessionTaskRunes    = 200
	readTasksToolName      = "read_tasks"
	readTasksDescription   = "Read the complete current session task list. Use before updating tasks when you need the latest checklist."
	updateTasksToolName    = "update_tasks"
	updateTasksDescription = "Create or replace the current session task list for multi-step work. Use for complex tasks to track progress; skip for simple single-step tasks. Always include every task that should remain, and keep at most one task in_progress."
)

type taskSessionManager interface {
	Get(id string) (*conversation.Session, bool)
	SetTasks(id string, tasks []conversation.Task) (*conversation.Session, error)
}

type sessionUpdateNotifier func(context.Context, *conversation.Session)

type updateTasksTool struct {
	sessions taskSessionManager
	notify   sessionUpdateNotifier
}

type readTasksTool struct {
	sessions taskSessionManager
}

type updateTasksParams struct {
	Tasks []conversation.Task `json:"tasks"`
}

type readTasksResponse struct {
	Tasks      []conversation.Task `json:"tasks"`
	Total      int                 `json:"total"`
	Completed  int                 `json:"completed"`
	InProgress string              `json:"in_progress,omitempty"`
}

type updateTasksResponse struct {
	Total         int      `json:"total"`
	Completed     int      `json:"completed"`
	InProgress    string   `json:"in_progress,omitempty"`
	JustCompleted []string `json:"just_completed,omitempty"`
}

func NewReadTasksTool(sessions taskSessionManager) Tool {
	return &readTasksTool{sessions: sessions}
}

func NewUpdateTasksTool(sessions taskSessionManager, notify sessionUpdateNotifier) Tool {
	return &updateTasksTool{sessions: sessions, notify: notify}
}

func (t *readTasksTool) Name() string        { return readTasksToolName }
func (t *readTasksTool) Exposure() Exposure  { return ExposureDirect }
func (t *readTasksTool) Description() string { return readTasksDescription }

func (t *readTasksTool) Spec() []byte {
	return []byte(`{
		"type":"object",
		"properties":{},
		"additionalProperties":false
	}`)
}

func (t *readTasksTool) Run(ctx context.Context, call Call) (Result, error) {
	if t.sessions == nil {
		return errResult("session task storage is unavailable"), nil
	}
	sessionID := SessionIDFromContext(ctx)
	if sessionID == "" {
		return errResult("session id is required"), nil
	}
	current, ok := t.sessions.Get(sessionID)
	if !ok {
		return errResult("session not found"), nil
	}
	response := readTasksResponse{
		Tasks: append([]conversation.Task(nil), current.Tasks...),
	}
	if response.Tasks == nil {
		response.Tasks = []conversation.Task{}
	}
	response.Total = len(response.Tasks)
	for _, task := range response.Tasks {
		if task.Status == conversation.TaskStatusCompleted {
			response.Completed++
		}
		if task.Status == conversation.TaskStatusInProgress {
			response.InProgress = task.Content
		}
	}
	data, _ := json.Marshal(response)
	return textResult(string(data)), nil
}

func (t *updateTasksTool) Name() string        { return updateTasksToolName }
func (t *updateTasksTool) Exposure() Exposure  { return ExposureDirect }
func (t *updateTasksTool) Description() string { return updateTasksDescription }

func (t *updateTasksTool) Spec() []byte {
	return []byte(`{
		"type":"object",
		"properties":{
			"tasks":{
				"type":"array",
				"maxItems":100,
				"description":"The complete task list that should remain for this session.",
				"items":{
					"type":"object",
					"properties":{
						"content":{"type":"string","minLength":1,"description":"Concise task description, 200 characters or fewer."},
						"status":{"type":"string","enum":["pending","in_progress","completed"]}
					},
					"required":["content","status"],
					"additionalProperties":false
				}
			}
		},
		"required":["tasks"],
		"additionalProperties":false
	}`)
}

func (t *updateTasksTool) Run(ctx context.Context, call Call) (Result, error) {
	if t.sessions == nil {
		return errResult("session task storage is unavailable"), nil
	}
	sessionID := SessionIDFromContext(ctx)
	if sessionID == "" {
		return errResult("session id is required"), nil
	}
	current, ok := t.sessions.Get(sessionID)
	if !ok {
		return errResult("session not found"), nil
	}
	var params updateTasksParams
	if err := json.Unmarshal(call.Input, &params); err != nil {
		return errResult("invalid arguments: " + err.Error()), nil
	}
	tasks, err := normalizeTasks(params.Tasks)
	if err != nil {
		return errResult(err.Error()), nil
	}
	oldStatus := make(map[string]conversation.TaskStatus, len(current.Tasks))
	for _, task := range current.Tasks {
		oldStatus[task.Content] = task.Status
	}
	updated, err := t.sessions.SetTasks(sessionID, tasks)
	if err != nil {
		return errResult("update tasks failed: " + err.Error()), nil
	}
	if t.notify != nil {
		t.notify(ctx, updated)
	}
	response := summarizeTasks(tasks, oldStatus)
	data, _ := json.Marshal(response)
	return textResult(string(data)), nil
}

func normalizeTasks(input []conversation.Task) ([]conversation.Task, error) {
	if len(input) > maxSessionTasks {
		return nil, fmt.Errorf("tasks may contain at most %d items", maxSessionTasks)
	}
	out := make([]conversation.Task, 0, len(input))
	inProgress := 0
	for i, item := range input {
		content := strings.Join(strings.Fields(item.Content), " ")
		if content == "" {
			return nil, fmt.Errorf("task %d content is required", i+1)
		}
		if len([]rune(content)) > maxSessionTaskRunes {
			return nil, fmt.Errorf("task %d content must be %d characters or fewer", i+1, maxSessionTaskRunes)
		}
		switch item.Status {
		case conversation.TaskStatusPending, conversation.TaskStatusInProgress, conversation.TaskStatusCompleted:
		default:
			return nil, fmt.Errorf("task %d has invalid status %q", i+1, item.Status)
		}
		if item.Status == conversation.TaskStatusInProgress {
			inProgress++
		}
		out = append(out, conversation.Task{
			Content: content,
			Status:  item.Status,
		})
	}
	if inProgress > 1 {
		return nil, fmt.Errorf("at most one task may be in_progress")
	}
	return out, nil
}

func summarizeTasks(tasks []conversation.Task, oldStatus map[string]conversation.TaskStatus) updateTasksResponse {
	response := updateTasksResponse{Total: len(tasks)}
	for _, task := range tasks {
		if task.Status == conversation.TaskStatusCompleted {
			response.Completed++
			if old, ok := oldStatus[task.Content]; ok && old != conversation.TaskStatusCompleted {
				response.JustCompleted = append(response.JustCompleted, task.Content)
			}
		}
		if task.Status == conversation.TaskStatusInProgress {
			response.InProgress = task.Content
		}
	}
	return response
}
