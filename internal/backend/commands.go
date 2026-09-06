package backend

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/freesoulcode/foya/internal/command"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/session"
	"github.com/freesoulcode/foya/internal/workflow"
)

// CommandExecution is the stable result returned after a user explicitly
// invokes a command.
type CommandExecution struct {
	Command    command.Command  `json:"command"`
	Status     string           `json:"status"`
	Submission *Submission      `json:"submission,omitempty"`
	Workflow   *workflow.Record `json:"workflow,omitempty"`
	Message    string           `json:"message,omitempty"`
}

type WorkflowApproval struct {
	Workflow   workflow.Record `json:"workflow"`
	Submission Submission      `json:"submission"`
}

func (b *Backend) commandManager() (*command.Manager, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.commands == nil {
		return nil, errors.New("commands are unavailable")
	}
	return b.commands, nil
}

func (b *Backend) commandProject(projectID string) (string, error) {
	if projectID == "" {
		return "", nil
	}
	item, err := b.Project(projectID)
	if err != nil {
		return "", err
	}
	return item.Path, nil
}

func (b *Backend) workflowManager() (*workflow.Manager, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.workflows == nil {
		return nil, errors.New("workflows are unavailable")
	}
	return b.workflows, nil
}

// Commands returns exactly one management scope, or the effective command
// surface for scope "effective".
func (b *Backend) Commands(
	ctx context.Context,
	scope string,
	projectID string,
) ([]command.Command, error) {
	manager, err := b.commandManager()
	if err != nil {
		return nil, err
	}
	projectPath, err := b.commandProject(projectID)
	if err != nil {
		return nil, err
	}
	switch command.Scope(scope) {
	case command.ScopeGlobal:
		return manager.ListScope(ctx, command.ScopeGlobal, "", "")
	case command.ScopeProject:
		return manager.ListScope(ctx, command.ScopeProject, projectID, projectPath)
	default:
		if scope == "effective" {
			return manager.List(ctx, projectID, projectPath)
		}
		return nil, command.ErrInvalidScope
	}
}

func (b *Backend) SessionCommands(ctx context.Context, sessionID string) ([]command.Command, error) {
	s, ok := b.sessions.Get(sessionID)
	if !ok {
		return nil, session.ErrNotFound
	}
	items, err := b.Commands(ctx, "effective", s.ProjectID)
	if err != nil {
		return nil, err
	}
	if s.ProjectID != "" {
		return items, nil
	}
	out := make([]command.Command, 0, len(items))
	for _, item := range items {
		if item.Ref != "builtin:spec" {
			out = append(out, item)
		}
	}
	return out, nil
}

func (b *Backend) CreateCommand(
	ctx context.Context,
	input command.CreateInput,
) (command.Command, error) {
	manager, err := b.commandManager()
	if err != nil {
		return command.Command{}, err
	}
	projectPath, err := b.commandProject(input.ProjectID)
	if err != nil {
		return command.Command{}, err
	}
	return manager.Create(ctx, input, projectPath)
}

func (b *Backend) UpdateCommand(
	ctx context.Context,
	ref string,
	input command.UpdateInput,
) (command.Command, error) {
	manager, err := b.commandManager()
	if err != nil {
		return command.Command{}, err
	}
	projectPath, err := b.commandProject(input.ProjectID)
	if err != nil {
		return command.Command{}, err
	}
	return manager.Update(ctx, ref, input, projectPath)
}

func (b *Backend) DeleteCommand(
	ctx context.Context,
	ref string,
	scope command.Scope,
	projectID string,
) error {
	manager, err := b.commandManager()
	if err != nil {
		return err
	}
	projectPath, err := b.commandProject(projectID)
	if err != nil {
		return err
	}
	return manager.Delete(ctx, scope, projectID, projectPath, ref)
}

// ExecuteCommand resolves a command in the session project. Prompt commands
// become normal turns; builtin workflows install durable workflow state before
// their first constrained Agent Loop turn.
func (b *Backend) ExecuteCommand(
	ctx context.Context,
	sessionID, name, args string,
) (CommandExecution, error) {
	s, ok := b.sessions.Get(sessionID)
	if !ok {
		return CommandExecution{}, session.ErrNotFound
	}
	manager, err := b.commandManager()
	if err != nil {
		return CommandExecution{}, err
	}
	projectPath, err := b.commandProject(s.ProjectID)
	if err != nil {
		return CommandExecution{}, err
	}
	item, err := manager.Resolve(ctx, s.ProjectID, projectPath, name)
	if err != nil {
		return CommandExecution{}, err
	}

	switch item.Kind {
	case command.KindPrompt:
		submission, err := b.SubmitTurn(ctx, sessionID, command.Expand(item.Body, args))
		if err != nil {
			return CommandExecution{}, err
		}
		return CommandExecution{Command: item, Status: "submitted", Submission: &submission}, nil
	case command.KindWorkflow:
		manager, err := b.workflowManager()
		if err != nil {
			return CommandExecution{}, err
		}
		if item.Name == string(workflow.KindSpec) && projectPath == "" {
			return CommandExecution{}, errors.New("spec workflow requires a project")
		}
		record, err := manager.Start(sessionID, workflow.Kind(item.Name), args, projectPath)
		if err != nil {
			return CommandExecution{}, err
		}
		if record.Kind == workflow.KindPlan {
			if _, err := b.sessions.SetAgentMode(sessionID, session.AgentModePlan, session.AgentModeExecute); err != nil {
				return CommandExecution{}, err
			}
			if updated, ok := b.sessions.Get(sessionID); ok {
				b.broadcastSession(ctx, updated)
			}
		}
		b.broadcastWorkflow(ctx, record)
		prompt := args
		if record.Kind == workflow.KindGoal {
			prompt = "Define a durable, measurable goal with constraints and success criteria. Do not implement code.\n\nObjective:\n" + args
		}
		submission, err := b.SubmitInput(ctx, sessionID, message.UserInput{Text: prompt, Command: item.Name})
		if err != nil {
			return CommandExecution{}, err
		}
		return CommandExecution{Command: item, Workflow: &record, Status: "submitted", Submission: &submission}, nil
	default:
		return CommandExecution{}, fmt.Errorf("unsupported command kind %q", item.Kind)
	}
}

func (b *Backend) CompleteWorkflow(sessionID, content string) error {
	manager, err := b.workflowManager()
	if err != nil {
		return err
	}
	record, completed, err := manager.CompleteActive(sessionID, content)
	if err != nil || !completed {
		return err
	}
	if record.Kind == workflow.KindPlan {
		if _, err := b.sessions.SetAgentMode(sessionID, session.AgentModePlanReady, session.AgentModeExecute); err != nil {
			return err
		}
	} else if record.Kind == workflow.KindGoal {
		record, err = manager.Approve(record.ID)
		if err != nil {
			return err
		}
	}
	if updated, ok := b.sessions.Get(sessionID); ok {
		b.broadcastSession(context.Background(), updated)
	}
	b.broadcastWorkflow(context.Background(), record)
	return nil
}

func (b *Backend) Workflow(sessionID string) (workflow.Record, bool, error) {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return workflow.Record{}, false, session.ErrNotFound
	}
	manager, err := b.workflowManager()
	if err != nil {
		return workflow.Record{}, false, err
	}
	record, ok := manager.Active(sessionID)
	return record, ok, nil
}

func (b *Backend) ApproveWorkflow(ctx context.Context, sessionID, id string) (WorkflowApproval, error) {
	manager, err := b.workflowManager()
	if err != nil {
		return WorkflowApproval{}, err
	}
	current, ok := manager.Get(id)
	if !ok || current.SessionID != sessionID {
		return WorkflowApproval{}, workflow.ErrNotFound
	}
	if (current.Kind != workflow.KindPlan && current.Kind != workflow.KindSpec) ||
		current.Status != workflow.StatusReady {
		return WorkflowApproval{}, workflow.ErrInvalidStatus
	}
	var specTasks []session.Task
	if current.Kind == workflow.KindSpec {
		specTasks, err = manager.SpecTaskItems(current.ID)
		if err != nil {
			return WorkflowApproval{}, err
		}
	}
	record, err := manager.Approve(id)
	if err != nil {
		return WorkflowApproval{}, err
	}
	if record.Kind == workflow.KindPlan {
		if _, err := b.sessions.SetAgentMode(sessionID, session.AgentModeExecute, ""); err != nil {
			return WorkflowApproval{}, err
		}
	}
	if record.Kind == workflow.KindSpec {
		if _, err := b.sessions.SetTasks(sessionID, specTasks); err != nil {
			return WorkflowApproval{}, err
		}
	}
	if updated, ok := b.sessions.Get(sessionID); ok {
		b.broadcastSession(ctx, updated)
	}
	b.broadcastWorkflow(ctx, record)
	executionPrompt := ""
	if record.Kind == workflow.KindSpec {
		executionPrompt = fmt.Sprintf(
			`Implement the approved specification now.

Read these files before making changes:
- Specification: %s
- Tasks: %s
- Acceptance checklist: %s

Use update_tasks to track the complete task list. Keep tasks.md checkboxes synchronized as tasks complete. Verify every checklist item and update checklist.md only when the criterion is actually satisfied. Do not create another specification or wait for another confirmation.`,
			record.Artifacts.Spec,
			record.Artifacts.Tasks,
			record.Artifacts.Checklist,
		)
	} else {
		executionPrompt = fmt.Sprintf(
			"Execute the approved plan at %s now. Do not create another plan and do not wait for another confirmation.",
			record.Path,
		)
	}
	submission, err := b.SubmitInput(ctx, sessionID, message.UserInput{
		Text:    executionPrompt,
		Command: string(record.Kind),
	})
	if err != nil {
		return WorkflowApproval{}, err
	}
	return WorkflowApproval{Workflow: record, Submission: submission}, nil
}

func (b *Backend) broadcastWorkflow(ctx context.Context, record workflow.Record) {
	ev := event.Event{Kind: event.KindWorkflowUpdated, Session: record.SessionID, Time: time.Now(), Payload: record}
	seq, err := b.log.Append(ctx, ev)
	if err != nil {
		return
	}
	ev.Seq = seq
	_ = b.bus.PublishMustDeliver(ctx, "session:"+record.SessionID, ev)
}
