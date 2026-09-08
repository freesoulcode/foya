package agent

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	conversation "github.com/freesoulcode/foya/internal/conversation"
	"github.com/freesoulcode/foya/internal/hooks"

	"github.com/freesoulcode/foya/internal/tool"
)

// runHook dispatches a synchronous lifecycle hook and persists one audit event
// per configured command. Hook command failures are represented in those audit
// events but intentionally fail open.
func (e *Engine) runHook(ctx context.Context, sessionID string, request hooks.Request) hooks.Outcome {
	e.mu.RLock()
	runtime := e.hooks
	e.mu.RUnlock()
	if runtime == nil {
		return hooks.Outcome{Decision: hooks.DecisionNone}
	}
	request = e.prepareHookRequest(ctx, sessionID, request)
	outcome := runtime.RunWithCompletion(ctx, request, func(asyncOutcome hooks.Outcome) {
		e.recordHookResults(hookAuditContext(request.RunID), sessionID, asyncOutcome.Results)
	})
	e.recordHookResults(context.WithoutCancel(ctx), sessionID, outcome.Results)
	return outcome
}

func (e *Engine) observeHook(ctx context.Context, sessionID string, request hooks.Request) {
	e.mu.RLock()
	runtime := e.hooks
	e.mu.RUnlock()
	if runtime == nil {
		return
	}
	request = e.prepareHookRequest(ctx, sessionID, request)
	runtime.Observe(context.WithoutCancel(ctx), request, func(outcome hooks.Outcome) {
		e.recordHookResults(hookAuditContext(request.RunID), sessionID, outcome.Results)
	})
}

func (e *Engine) prepareHookRequest(
	ctx context.Context,
	sessionID string,
	request hooks.Request,
) hooks.Request {
	if request.EventID == "" {
		request.EventID = newRunID()
	}
	if request.OccurredAt.IsZero() {
		request.OccurredAt = time.Now().UTC()
	}
	request.SessionID = sessionID
	request.RootSessionID = e.rootSessionID(sessionID)
	request.CWD = tool.CWDFromContext(ctx)
	request.ProjectID = e.resolveProjectID(sessionID)
	request.ProjectPath = e.resolveProjectPath(sessionID)
	if request.CWD == "" {
		request.CWD = request.ProjectPath
	}
	request.Model = e.currentModel(sessionID)
	return request
}

func hookAuditContext(runID string) context.Context {
	return tool.WithRunID(context.Background(), runID)
}

// notifyHook dispatches Notification hooks without waiting for their command
// processes. Completion remains observable through hook_completed events.
func (e *Engine) notifyHook(ctx context.Context, sessionID string, request hooks.Request) {
	e.mu.RLock()
	runtime := e.hooks
	e.mu.RUnlock()
	if runtime == nil {
		return
	}
	request.Event = hooks.EventNotification
	request = e.prepareHookRequest(ctx, sessionID, request)
	runtime.Notify(context.WithoutCancel(ctx), request, func(outcome hooks.Outcome) {
		e.recordHookResults(hookAuditContext(request.RunID), sessionID, outcome.Results)
	})
}

func (e *Engine) recordHookResults(ctx context.Context, sessionID string, results []hooks.Result) {
	for _, result := range results {
		e.emit(ctx, sessionID, conversation.KindHookCompleted, map[string]any{
			"id":            result.ID,
			"event_id":      result.EventID,
			"name":          result.Name,
			"event":         result.Event,
			"decision":      result.Decision,
			"halt":          result.Halt,
			"reason":        result.Reason,
			"duration_ms":   result.Duration.Milliseconds(),
			"error":         result.Error,
			"input_rewrite": len(result.UpdatedInput) > 0,
		}, true)
	}
}

// RunSessionStart executes SessionStart hooks once the session exists and
// stores returned context as a system message for the first model request.
func (e *Engine) RunSessionStart(ctx context.Context, sessionID string) {
	if _, ok := e.sessions.Get(sessionID); !ok {
		return
	}
	ctx = tool.WithCWD(ctx, e.resolveProjectPath(sessionID))
	ctx = tool.WithSessionID(ctx, sessionID)
	outcome := e.runHook(ctx, sessionID, hooks.Request{
		Event:  hooks.EventSessionStart,
		Source: "startup",
	})
	e.appendHookContext(ctx, sessionID, outcome.Context, "session_start")
}

// RunSessionEnd notifies observational hooks before a session is removed.
func (e *Engine) RunSessionEnd(ctx context.Context, sessionID, reason string) {
	e.observeHook(ctx, sessionID, hooks.Request{
		Event:  hooks.EventSessionEnd,
		Source: reason,
		Reason: reason,
	})
}

// RunSubagentStart dispatches lifecycle hooks after the child session exists.
func (e *Engine) RunSubagentStart(
	ctx context.Context,
	sessionID, parentSessionID, runID, agentID, agentType, task string,
) {
	outcome := e.runHook(ctx, sessionID, hooks.Request{
		Event:           hooks.EventSubagentStart,
		RunID:           runID,
		AgentID:         agentID,
		AgentType:       agentType,
		ParentSessionID: parentSessionID,
		ChildSessionID:  sessionID,
		Metadata:        map[string]any{"task": task},
	})
	e.appendHookContext(ctx, sessionID, outcome.Context, "subagent_start")
}

// RunSubagentStop dispatches a child completion hook. It is synchronous so a
// hook may provide final validation feedback before the parent receives output.
func (e *Engine) RunSubagentStop(
	ctx context.Context,
	sessionID, parentSessionID, runID, agentID, agentType, status, output, errorText string,
) (bool, string) {
	outcome := e.runHook(ctx, sessionID, hooks.Request{
		Event:            hooks.EventSubagentStop,
		RunID:            runID,
		AgentID:          agentID,
		AgentType:        agentType,
		ParentSessionID:  parentSessionID,
		ChildSessionID:   sessionID,
		Status:           status,
		Error:            errorText,
		AssistantMessage: output,
	})
	if outcome.Decision == hooks.DecisionDeny || outcome.Halt {
		return true, hookFeedback(outcome, "sub-agent completion blocked by hook")
	}
	return false, ""
}

func (e *Engine) appendHookContext(ctx context.Context, sessionID string, contexts []string, source string) {
	text := strings.TrimSpace(strings.Join(contexts, "\n"))
	if text == "" {
		return
	}
	e.emit(ctx, sessionID, conversation.KindMessageEnd, conversation.Message{
		Role:    conversation.RoleSystem,
		Content: "<hook_context source=\"" + source + "\">\n" + text + "\n</hook_context>",
	}, true)
}

func hookFeedback(outcome hooks.Outcome, fallback string) string {
	parts := make([]string, 0, len(outcome.Context)+1)
	if reason := strings.TrimSpace(outcome.Reason); reason != "" {
		parts = append(parts, reason)
	}
	parts = append(parts, outcome.Context...)
	text := strings.TrimSpace(strings.Join(parts, "\n"))
	if text == "" {
		return fallback
	}
	return text
}

func appendHookText(result *tool.Result, contexts []string) {
	text := strings.TrimSpace(strings.Join(contexts, "\n"))
	if text == "" {
		return
	}
	result.Content = append(result.Content, tool.ContentPart{
		Type: "text",
		Text: "<hook_context>\n" + text + "\n</hook_context>",
	})
}

func hookRequest(
	eventName hooks.Event,
	runID string,
	userPrompt string,
	call conversation.ToolCall,
	toolOutput string,
	assistantMessage string,
	notification string,
) hooks.Request {
	return hooks.Request{
		Event:            eventName,
		RunID:            runID,
		UserPrompt:       userPrompt,
		ToolName:         call.Name,
		ToolCallID:       call.ID,
		ToolInput:        append(json.RawMessage(nil), call.Input...),
		ToolOutput:       toolOutput,
		AssistantMessage: assistantMessage,
		Notification:     notification,
	}
}
