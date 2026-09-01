package agent

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/hooks"
	"github.com/freesoulcode/foya/internal/message"
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
	request.SessionID = sessionID
	request.CWD = tool.CWDFromContext(ctx)
	request.ProjectPath = e.resolveProjectPath(sessionID)
	request.Model = e.currentModel(sessionID)
	outcome := runtime.Run(ctx, request)
	e.recordHookResults(context.WithoutCancel(ctx), sessionID, outcome.Results)
	return outcome
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
	request.SessionID = sessionID
	request.CWD = tool.CWDFromContext(ctx)
	request.ProjectPath = e.resolveProjectPath(sessionID)
	request.Model = e.currentModel(sessionID)
	runtime.Notify(context.WithoutCancel(ctx), request, func(outcome hooks.Outcome) {
		e.recordHookResults(context.Background(), sessionID, outcome.Results)
	})
}

func (e *Engine) recordHookResults(ctx context.Context, sessionID string, results []hooks.Result) {
	for _, result := range results {
		e.emit(ctx, sessionID, event.KindHookCompleted, map[string]any{
			"id":            result.ID,
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
		Event: hooks.EventSessionStart,
	})
	e.appendHookContext(ctx, sessionID, outcome.Context, "session_start")
}

func (e *Engine) appendHookContext(ctx context.Context, sessionID string, contexts []string, source string) {
	text := strings.TrimSpace(strings.Join(contexts, "\n"))
	if text == "" {
		return
	}
	e.emit(ctx, sessionID, event.KindMessageEnd, message.Message{
		Role:    message.RoleSystem,
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
	call message.ToolCall,
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
