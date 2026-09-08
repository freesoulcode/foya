package agent

import (
	"context"

	"encoding/json"
	"errors"

	"strings"
	"sync"
	"time"

	conversation "github.com/freesoulcode/foya/internal/conversation"
	"github.com/freesoulcode/foya/internal/hooks"

	modelapi "github.com/freesoulcode/foya/internal/model"

	foyatelemetry "github.com/freesoulcode/foya/internal/telemetry"

	"github.com/freesoulcode/foya/internal/tool"
	workflow "github.com/freesoulcode/foya/internal/workflow"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// executeToolCalls dispatches parallel-safe calls on a bounded concurrent lane
// while preserving model order for calls on the sequential lane. Result ordering
// always matches the model's tool-call ordering.
func (e *Engine) executeToolCalls(
	ctx context.Context,
	sessionID string,
	calls []conversation.ToolCall,
	guard *loopGuard,
) []executedToolCall {
	results := make([]executedToolCall, len(calls))
	parallelIndexes := make([]int, 0, len(calls))
	sequentialIndexes := make([]int, 0, len(calls))
	for i, call := range calls {
		results[i] = executedToolCall{call: call, sig: callSig(call.Name, call.Input)}
		if e.toolExecutionMode(sessionID, call.Name) == "parallel" {
			parallelIndexes = append(parallelIndexes, i)
		} else {
			sequentialIndexes = append(sequentialIndexes, i)
		}
	}

	run := func(i int) {
		item := &results[i]
		if ctx.Err() != nil {
			item.output = "Interrupted"
			return
		}
		spanStartedAt := time.Now()
		spanAttrs := []attribute.KeyValue{
			attribute.String("session.id", sessionID),
			attribute.String("langfuse.session.id", e.rootSessionID(sessionID)),
			attribute.String("langfuse.trace.name", "foya.turn"),
			attribute.String("langfuse.observation.type", "tool"),
			attribute.String("foya.run.id", tool.RunIDFromContext(ctx)),
			attribute.String("gen_ai.tool.name", item.call.Name),
			attribute.String("gen_ai.tool.call.id", item.call.ID),
		}
		if foyatelemetry.CaptureContent() {
			spanAttrs = append(spanAttrs,
				attribute.String("gen_ai.tool.call.arguments", foyatelemetry.Content(string(item.call.Input))),
				attribute.String("langfuse.observation.input", foyatelemetry.Content(string(item.call.Input))),
			)
		}
		toolCtx, toolSpan := foyatelemetry.StartSpan(
			ctx,
			"foya.tool",
			trace.SpanKindInternal,
			spanAttrs...,
		)
		defer func() {
			status := "completed"
			var spanErr error
			if item.isErr {
				status = "failed"
				spanErr = errors.New("tool execution failed")
			}
			finalAttrs := append([]attribute.KeyValue(nil), spanAttrs...)
			finalAttrs = append(finalAttrs, attribute.String("foya.tool.status", status))
			if foyatelemetry.CaptureContent() {
				finalAttrs = append(finalAttrs,
					attribute.String("gen_ai.tool.call.result", foyatelemetry.Content(item.output)),
					attribute.String("langfuse.observation.output", foyatelemetry.Content(item.output)),
				)
			}
			foyatelemetry.EndSpan(toolSpan, status, spanErr, finalAttrs...)
			foyatelemetry.RecordTool(
				toolCtx,
				time.Since(spanStartedAt),
				attribute.String("gen_ai.tool.name", item.call.Name),
				attribute.String("foya.tool.status", status),
			)
		}()
		if guard.blockBeforeExec(item.sig) {
			item.output = loopGateText(item.call.Name)
			item.isErr = true
			e.emit(toolCtx, sessionID, conversation.KindToolUpdate, toolCallPayload{
				ID: item.call.ID, Name: item.call.Name, Output: item.output,
				IsError: true, Status: "error",
			}, true)
			return
		}
		preHook := e.runHook(toolCtx, sessionID, hookRequest(
			hooks.EventPreToolUse,
			tool.RunIDFromContext(toolCtx),
			"",
			item.call,
			"",
			"",
			"",
		))
		if preHook.Halt {
			item.terminate = true
		}
		if preHook.Decision == hooks.DecisionDeny {
			item.output = "Tool call blocked by a hook: " + hookFeedback(preHook, "Operation not allowed")
			item.isErr = true
			e.emit(toolCtx, sessionID, conversation.KindToolUpdate, toolCallPayload{
				ID: item.call.ID, Name: item.call.Name, Output: item.output,
				IsError: true, Status: "error",
			}, true)
			return
		}
		if len(preHook.UpdatedInput) > 0 {
			item.call.Input = preHook.UpdatedInput
		}
		e.emit(toolCtx, sessionID, conversation.KindToolUpdate, toolCallPayload{
			ID: item.call.ID, Name: item.call.Name, Input: string(item.call.Input), Status: "running",
		}, true)
		toolStartedAt := time.Now()
		result := e.executeTool(toolCtx, sessionID, item.call)
		toolCompletedAt := time.Now()
		appendHookText(&result, preHook.Context)
		postRequest := hookRequest(
			hooks.EventPostToolUse,
			tool.RunIDFromContext(toolCtx),
			"",
			item.call,
			resultText(result),
			"",
			"",
		)
		postRequest.Status = "completed"
		postRequest.StartedAt = &toolStartedAt
		postRequest.CompletedAt = &toolCompletedAt
		postRequest.DurationMS = toolCompletedAt.Sub(toolStartedAt).Milliseconds()
		postRequest.ToolIsError = result.IsError
		if result.IsError {
			postRequest.Status = "failed"
			postRequest.Error = resultText(result)
		}
		postHook := e.runHook(toolCtx, sessionID, postRequest)
		appendHookText(&result, postHook.Context)
		if postHook.Decision == hooks.DecisionDeny {
			result.IsError = true
			appendHookText(&result, []string{
				"Tool result rejected by a hook: " + hookFeedback(postHook, "Result did not meet requirements"),
			})
		}
		if postHook.Halt {
			item.terminate = true
		}
		item.output = resultText(result)
		item.attachments = e.persistToolImages(toolCtx, sessionID, item.call.Name, result)
		item.isErr = result.IsError
		item.diff = result.Diff
		item.fileChange = result.FileChange
		if toolCtx.Err() != nil {
			item.output = "Interrupted"
			item.isErr = false
		}
		status := "done"
		if item.isErr {
			status = "error"
		}
		e.emit(toolCtx, sessionID, conversation.KindToolUpdate, toolCallPayload{
			ID: item.call.ID, Name: item.call.Name, Output: item.output,
			Attachments: item.attachments, IsError: item.isErr, Diff: item.diff, Status: status,
		}, true)
	}

	var wg sync.WaitGroup
	parallelSlots := make(chan struct{}, maxParallelToolCalls)
	for _, index := range parallelIndexes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case parallelSlots <- struct{}{}:
				defer func() { <-parallelSlots }()
				run(index)
			case <-ctx.Done():
				results[index].output = "Interrupted"
			}
		}()
	}
	if len(sequentialIndexes) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for position, index := range sequentialIndexes {
				run(index)
				if ctx.Err() == nil {
					continue
				}
				for _, remaining := range sequentialIndexes[position+1:] {
					results[remaining].output = "Interrupted"
				}
				break
			}
		}()
	}
	wg.Wait()

	for i := range results {
		if results[i].output == "" {
			results[i].output = "(no output)"
		}
		if ctx.Err() == nil && results[i].output != loopGateText(results[i].call.Name) {
			guard.recordResult(results[i].sig, results[i].isErr)
		}
	}
	return results
}

func (e *Engine) toolExecutionMode(sessionID, name string) string {
	t, ok := e.tools.Get(name)
	parallel, parallelOK := t.(tool.ParallelTool)
	if ok && parallelOK && parallel.Parallel() && e.toolAllowed(sessionID, name) {
		return "parallel"
	}
	return "sequential"
}

// Cancel stops the active turn and propagates through providers, tools, and approvals.
func (e *Engine) Cancel(sessionID string) {
	e.CancelWithReason(sessionID, TurnCancelReasonUserStop)
}

func (e *Engine) CancelWithReason(sessionID string, reason TurnCancelReason) {
	if v, ok := e.cancels.Load(sessionID); ok {
		state := v.(*turnRunState)
		if reason == TurnCancelReasonUserStop {
			e.emitTurnCancelRequested(context.Background(), sessionID, state, reason)
		}
		state.cancel(turnCancelCause(reason))
	}
}

func (e *Engine) emitTurnCancelRequested(
	ctx context.Context,
	sessionID string,
	state *turnRunState,
	reason TurnCancelReason,
) {
	if state == nil || state.runID == "" {
		return
	}
	state.cancelRequestOnce.Do(func() {
		requestedAt := time.Now()
		e.emitEvent(context.WithoutCancel(ctx), conversation.Event{
			Kind:    conversation.KindTurnCancelRequested,
			Session: sessionID,
			RunID:   state.runID,
			Time:    requestedAt,
			Payload: turnCancelRequestedPayload{
				RunID:       state.runID,
				Reason:      string(reason),
				RequestedAt: requestedAt,
			},
		}, true)
	})
}

// CancelTool interrupts one active tool call while leaving the Agent Loop
// alive so the model can observe the interrupted result and choose a fallback.
func (e *Engine) CancelTool(sessionID, toolCallID string) bool {
	if cancel, ok := e.toolCancels.Load(toolCancelKey(sessionID, toolCallID)); ok {
		cancel.(context.CancelCauseFunc)(errToolCancelledByUser)
		return true
	}
	return false
}

// CancelAndWait stops a turn and waits for its goroutine to exit or time out.
func (e *Engine) CancelAndWait(sessionID string, timeout time.Duration) {
	v, ok := e.cancels.Load(sessionID)
	if !ok {
		return
	}
	v.(*turnRunState).cancel(ErrTurnCancelledBySessionDelete)
	if d, ok := e.dones.Load(sessionID); ok {
		select {
		case <-d.(chan struct{}):
		case <-time.After(timeout):
		}
	}
}

// executeTool resolves and runs one tool call.
func (e *Engine) executeTool(ctx context.Context, sessionID string, tc conversation.ToolCall) tool.Result {
	if !e.toolAllowed(sessionID, tc.Name) {
		return tool.Result{IsError: true, Content: []tool.ContentPart{{
			Type: "text", Text: "tool is not allowed for this agent: " + tc.Name,
		}}}
	}
	t, ok := e.tools.Get(tc.Name)
	if !ok {
		return tool.Result{IsError: true, Content: []tool.ContentPart{{Type: "text", Text: "unknown tool: " + tc.Name}}}
	}
	if t.Exposure() == tool.ExposureDeferred && !tool.ActiveToolFromContext(ctx, tc.Name) {
		return tool.Result{IsError: true, Content: []tool.ContentPart{{
			Type: "text",
			Text: "tool is deferred and has not been activated for this model step: " + tc.Name + ". Call tool_search first, then use the tool on the next step.",
		}}}
	}
	call := tool.Call{ID: tc.ID, Name: tc.Name, Input: tc.Input}
	toolCtx, cancel := context.WithCancelCause(ctx)
	key := toolCancelKey(sessionID, tc.ID)
	e.toolCancels.Store(key, cancel)
	defer e.toolCancels.Delete(key)
	defer cancel(nil)
	result, err := t.Run(toolCtx, call)
	if errors.Is(context.Cause(toolCtx), errToolCancelledByUser) {
		return tool.Result{IsError: true, Content: []tool.ContentPart{{
			Type: "text", Text: toolCancelledByUserResult,
		}}}
	}
	if err != nil {
		return tool.Result{IsError: true, Content: []tool.ContentPart{{Type: "text", Text: err.Error()}}}
	}
	return result
}

func toolCancelKey(sessionID, toolCallID string) string {
	return sessionID + "\x00" + toolCallID
}

func (e *Engine) toolDefsForSession(
	sessionID string,
	activeDeferred map[string]bool,
) []modelapi.ToolDef {
	defs := e.tools.SpecsFor(activeDeferred)
	policy, hasWorkflowPolicy := e.workflowPolicyForSession(sessionID)
	if hasWorkflowPolicy {
		for _, name := range policy.ExtraTools {
			if candidate, ok := e.tools.Get(name); ok {
				defs = appendToolDef(defs, candidate)
			}
		}
	}
	if e.sessions != nil {
		s, ok := e.sessions.Get(sessionID)
		if ok && s.AllowedTools != nil {
			allowed := make(map[string]bool, len(s.AllowedTools))
			for _, name := range s.AllowedTools {
				allowed[name] = true
			}
			filtered := defs[:0]
			for _, def := range defs {
				if allowed[def.Function.Name] {
					filtered = append(filtered, def)
				}
			}
			defs = filtered
		}
	}
	if hasWorkflowPolicy {
		return filterToolDefs(defs, policy.AllowedTools)
	}
	return defs
}

func appendToolDef(defs []modelapi.ToolDef, candidate tool.Tool) []modelapi.ToolDef {
	for _, def := range defs {
		if def.Function.Name == candidate.Name() {
			return defs
		}
	}
	parameters := json.RawMessage(candidate.Spec())
	if len(parameters) == 0 {
		parameters = json.RawMessage(`{"type":"object","properties":{}}`)
	}
	return append(defs, modelapi.ToolDef{
		Type: "function",
		Function: modelapi.FunctionDef{
			Name:        candidate.Name(),
			Description: candidate.Description(),
			Parameters:  parameters,
		},
	})
}

func (e *Engine) ensureHistoryResultToolDef(
	sessionID string,
	defs []modelapi.ToolDef,
) []modelapi.ToolDef {
	for _, def := range defs {
		if def.Function.Name == tool.HistoryReadToolResultName {
			return defs
		}
	}
	if !e.toolAllowed(sessionID, tool.HistoryReadToolResultName) {
		return defs
	}
	historyTool, ok := e.tools.Get(tool.HistoryReadToolResultName)
	if !ok {
		return defs
	}
	return appendToolDef(defs, historyTool)
}

func messagesContainToolResultReference(messages []conversation.Message) bool {
	for _, item := range messages {
		if strings.Contains(item.ModelContent(), tool.HistoryReadToolResultName) {
			return true
		}
	}
	return false
}

func activeToolDefNames(defs []modelapi.ToolDef) map[string]bool {
	out := make(map[string]bool, len(defs))
	for _, def := range defs {
		out[def.Function.Name] = true
	}
	return out
}

func (e *Engine) toolAllowed(sessionID, name string) bool {
	if name == tool.HistoryReadToolResultName {
		return true
	}
	if policy, ok := e.workflowPolicyForSession(sessionID); ok {
		if !toolNameAllowed(policy.AllowedTools, name) {
			return false
		}
	}
	if e.sessions == nil {
		return true
	}
	s, ok := e.sessions.Get(sessionID)
	if !ok || s.AllowedTools == nil {
		return true
	}
	for _, allowed := range s.AllowedTools {
		if allowed == name {
			return true
		}
	}
	return false
}

func (e *Engine) agentInstructions(sessionID string) string {
	var instructions []string
	if e.sessions == nil {
		if policy, ok := e.workflowPolicyForSession(sessionID); ok {
			return policy.Instructions
		}
		return ""
	}
	if s, ok := e.sessions.Get(sessionID); ok {
		instructions = append(instructions, s.AgentInstructions)
	}
	if policy, ok := e.workflowPolicyForSession(sessionID); ok {
		instructions = append(instructions, policy.Instructions)
	}
	return strings.TrimSpace(strings.Join(instructions, "\n\n"))
}

func (e *Engine) workflowPolicyForSession(sessionID string) (workflow.Policy, bool) {
	e.mu.RLock()
	resolve := e.workflowPolicy
	e.mu.RUnlock()
	if resolve == nil {
		return workflow.Policy{}, false
	}
	return resolve(sessionID)
}

func (e *Engine) completeWorkflow(sessionID, content string) error {
	if strings.TrimSpace(content) == "" {
		return nil
	}
	e.mu.RLock()
	complete := e.workflowComplete
	e.mu.RUnlock()
	if complete == nil {
		return nil
	}
	return complete(sessionID, content)
}

func filterToolDefs(defs []modelapi.ToolDef, allowed []string) []modelapi.ToolDef {
	filtered := make([]modelapi.ToolDef, 0, len(defs))
	for _, def := range defs {
		if toolNameAllowed(allowed, def.Function.Name) {
			filtered = append(filtered, def)
		}
	}
	return filtered
}

func toolNameAllowed(allowed []string, name string) bool {
	for _, item := range allowed {
		if item == name {
			return true
		}
	}
	return false
}
