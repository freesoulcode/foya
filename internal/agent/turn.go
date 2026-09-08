package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	conversation "github.com/freesoulcode/foya/internal/conversation"
	"github.com/freesoulcode/foya/internal/hooks"
	interaction "github.com/freesoulcode/foya/internal/interaction"

	modelapi "github.com/freesoulcode/foya/internal/model"

	foyatelemetry "github.com/freesoulcode/foya/internal/telemetry"
	"github.com/freesoulcode/foya/internal/tool"
)

// toolCallPayload is emitted for tool lifecycle events.
type toolCallPayload struct {
	ID          string                       `json:"id"`
	Name        string                       `json:"name"`
	Input       string                       `json:"input,omitempty"`
	Output      string                       `json:"output,omitempty"`
	Attachments []conversation.AttachmentRef `json:"attachments,omitempty"`
	Status      string                       `json:"status,omitempty"` // queued / running / done / error
	IsError     bool                         `json:"is_error,omitempty"`
	Diff        string                       `json:"diff,omitempty"` // Display-only file diff.
}

type turnStartedPayload struct {
	RunID     string    `json:"run_id"`
	StartedAt time.Time `json:"started_at"`
}

type turnCancelRequestedPayload struct {
	RunID       string    `json:"run_id"`
	Reason      string    `json:"reason"`
	RequestedAt time.Time `json:"requested_at"`
}

type turnCompletePayload struct {
	RunID       string    `json:"run_id"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
	Status      string    `json:"status"`
	Reason      string    `json:"reason,omitempty"`
}

func turnCompletionFromContext(ctx context.Context) (string, string) {
	if ctx.Err() == nil {
		return turnStatusCompleted, ""
	}
	return turnStatusCancelled, turnCancelReasonFromCause(context.Cause(ctx))
}

func turnCancelReasonFromCause(cause error) string {
	switch {
	case errors.Is(cause, ErrTurnCancelledByUser):
		return string(TurnCancelReasonUserStop)
	case errors.Is(cause, ErrTurnCancelledBySessionDelete):
		return string(TurnCancelReasonSessionDeleted)
	case errors.Is(cause, ErrTurnCancelledByQueueDispatch):
		return string(TurnCancelReasonQueueDispatch)
	default:
		return turnReasonContextCancelled
	}
}

func turnCancelCause(reason TurnCancelReason) error {
	switch reason {
	case TurnCancelReasonUserStop:
		return ErrTurnCancelledByUser
	case TurnCancelReasonSessionDeleted:
		return ErrTurnCancelledBySessionDelete
	case TurnCancelReasonQueueDispatch:
		return ErrTurnCancelledByQueueDispatch
	default:
		return context.Canceled
	}
}

type executedToolCall struct {
	call        conversation.ToolCall
	output      string
	attachments []conversation.AttachmentRef
	fileChange  *conversation.FileChange
	isErr       bool
	diff        string
	sig         string
	terminate   bool
}

type deferredToolState struct {
	mu     sync.Mutex
	active map[string]bool
}

func newDeferredToolState() *deferredToolState {
	return &deferredToolState{active: make(map[string]bool)}
}

func (s *deferredToolState) ActivateTool(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active[name] = true
}

func (s *deferredToolState) Snapshot() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]bool, len(s.active))
	for name, active := range s.active {
		out[name] = active
	}
	return out
}

// RunTurn executes a regular user turn.
func (e *Engine) RunTurn(ctx context.Context, sessionID, userText string) error {
	return e.RunInput(ctx, sessionID, conversation.UserInput{Text: userText})
}

func (e *Engine) RunInput(ctx context.Context, sessionID string, input conversation.UserInput) error {
	return e.runTurn(ctx, sessionID, input)
}

// InvalidateHistoryEstimate drops request-size baselines tied to a superseded
// conversation projection.
func (e *Engine) InvalidateHistoryEstimate(sessionID string) {
	e.requestBudgets.Delete(sessionID)
	prefix := sessionID + "\x00"
	e.acceptedBoundaries.Range(func(key, _ any) bool {
		if value, ok := key.(string); ok && strings.HasPrefix(value, prefix) {
			e.acceptedBoundaries.Delete(key)
		}
		return true
	})
	e.compactionFailures.Delete(sessionID)
}

// runTurn executes one potentially multi-step turn.
func (e *Engine) runTurn(
	ctx context.Context,
	sessionID string,
	input conversation.UserInput,
) error {

	runID := newRunID()
	turnStartedAt := time.Now()
	turnCtx, cancel := context.WithCancelCause(ctx)
	runState := &turnRunState{
		cancel: cancel,
		runID:  runID,
	}
	if _, loaded := e.cancels.LoadOrStore(sessionID, runState); loaded {
		cancel(context.Canceled)
		return fmt.Errorf("The chat already has a running turn")
	}

	done := make(chan struct{})
	e.dones.Store(sessionID, done)
	defer e.cancels.Delete(sessionID)
	defer e.dones.Delete(sessionID)
	defer close(done)
	defer cancel(nil)
	ctx = turnCtx
	ctx = tool.WithRunID(ctx, runID)
	model := e.currentModel(sessionID)
	prov := e.currentProvider(sessionID)
	userText := input.Text
	reasoningEffort := e.resolveReasoningEffort(sessionID)
	projectPath := e.resolveProjectPath(sessionID)
	turnAttrs := []attribute.KeyValue{
		attribute.String("session.id", sessionID),
		attribute.String("langfuse.session.id", e.rootSessionID(sessionID)),
		attribute.String("langfuse.trace.name", "foya.turn"),
		attribute.String("foya.run.id", runID),
		attribute.String("foya.project.id", e.resolveProjectID(sessionID)),
		attribute.String("gen_ai.request.model", model),
	}
	if input.SkillRef != "" {
		turnAttrs = append(turnAttrs, attribute.String("foya.skill.ref", input.SkillRef))
	}
	if prov != nil {
		turnAttrs = append(turnAttrs, attribute.String("gen_ai.system", prov.Name()))
	}
	if foyatelemetry.CaptureContent() {
		turnAttrs = append(turnAttrs,
			attribute.String("foya.turn.input", foyatelemetry.Content(userText)),
			attribute.String("langfuse.trace.input", foyatelemetry.Content(userText)),
		)
	}
	ctx, turnSpan := foyatelemetry.StartSpan(ctx, "foya.turn", trace.SpanKindInternal, turnAttrs...)
	turnSpanEnded := false
	defer func() {
		if !turnSpanEnded {
			foyatelemetry.EndSpan(
				turnSpan,
				turnStatusFailed,
				errors.New("turn exited without completion"),
			)
		}
	}()
	terminalAssistantRecorded := false
	finalAssistantMessage := ""
	turnUsage := modelapi.Usage{Model: model}
	recordPartialAssistant := func(content, reasoning, status, reason string) {
		if terminalAssistantRecorded ||
			strings.TrimSpace(content) == "" && strings.TrimSpace(reasoning) == "" {
			return
		}
		completedAt := time.Now()
		e.emit(context.WithoutCancel(ctx), sessionID, conversation.KindMessageEnd, conversation.Message{
			Role:            conversation.RoleAssistant,
			Content:         content,
			Reasoning:       reasoning,
			TurnStartedAt:   &turnStartedAt,
			TurnCompletedAt: &completedAt,
			TurnStatus:      status,
			TurnReason:      reason,
		}, true)
		finalAssistantMessage = content
		terminalAssistantRecorded = true
	}
	completeTurn := func(status, reason string) {
		if status == "" {
			status, reason = turnCompletionFromContext(ctx)
		}
		if status == turnStatusCancelled && reason == string(TurnCancelReasonUserStop) {
			e.emitTurnCancelRequested(ctx, sessionID, runState, TurnCancelReasonUserStop)
		}
		completedAt := time.Now()
		persistCtx := context.WithoutCancel(ctx)
		if status == turnStatusCancelled && !terminalAssistantRecorded {
			msg := conversation.Message{
				Role:            conversation.RoleAssistant,
				TurnStartedAt:   &turnStartedAt,
				TurnCompletedAt: &completedAt,
				TurnStatus:      status,
				TurnReason:      reason,
			}
			e.emit(persistCtx, sessionID, conversation.KindMessageEnd, msg, true)
			terminalAssistantRecorded = true
		}
		e.emit(persistCtx, sessionID, conversation.KindTurnComplete, turnCompletePayload{
			RunID:       runID,
			StartedAt:   turnStartedAt,
			CompletedAt: completedAt,
			Status:      status,
			Reason:      reason,
		}, true)
		e.observeHook(persistCtx, sessionID, hooks.Request{
			Event:            hooks.EventTurnComplete,
			RunID:            runID,
			Status:           status,
			Reason:           reason,
			StartedAt:        &turnStartedAt,
			CompletedAt:      &completedAt,
			DurationMS:       completedAt.Sub(turnStartedAt).Milliseconds(),
			AssistantMessage: finalAssistantMessage,
			Metadata: map[string]any{
				"usage": turnUsage,
			},
		})
		e.notifyHook(persistCtx, sessionID, hookRequest(
			hooks.EventNotification,
			runID,
			"",
			conversation.ToolCall{},
			"",
			"",
			"turn_complete",
		))
		finalAttrs := append([]attribute.KeyValue(nil), turnAttrs...)
		finalAttrs = append(finalAttrs,
			attribute.String("foya.turn.status", status),
			attribute.String("foya.turn.reason", reason),
			attribute.Int64("gen_ai.usage.input_tokens", turnUsage.InputTokens),
			attribute.Int64("gen_ai.usage.output_tokens", turnUsage.OutputTokens),
			attribute.Int64("foya.usage.cached_input_tokens", turnUsage.CachedTokens),
		)
		if foyatelemetry.CaptureContent() {
			finalAttrs = append(finalAttrs,
				attribute.String("foya.turn.output", foyatelemetry.Content(finalAssistantMessage)),
				attribute.String("langfuse.trace.output", foyatelemetry.Content(finalAssistantMessage)),
			)
		}
		foyatelemetry.EndSpan(turnSpan, status, nil, finalAttrs...)
		metricAttrs := []attribute.KeyValue{
			attribute.String("foya.turn.status", status),
			attribute.String("gen_ai.request.model", model),
		}
		if prov != nil {
			metricAttrs = append(metricAttrs, attribute.String("gen_ai.system", prov.Name()))
		}
		foyatelemetry.RecordTurn(persistCtx, completedAt.Sub(turnStartedAt), metricAttrs...)
		turnSpanEnded = true
	}
	e.setSessionPhase(ctx, sessionID, conversation.PhaseTurn)
	defer e.setSessionPhase(context.WithoutCancel(ctx), sessionID, conversation.PhaseIdle)

	if e.sessions != nil {
		ctx = tool.WithCWD(ctx, projectPath)
		ctx = tool.WithProjectID(ctx, e.resolveProjectID(sessionID))
		ctx = tool.WithSessionID(ctx, sessionID)
		ctx = tool.WithModelRuntime(ctx, tool.ModelRuntime{Provider: prov, Model: model})
		ctx = interaction.WithMode(ctx, e.resolveApprovalMode(sessionID))
		ctx = interaction.WithSession(ctx, e.approvalEventSession(sessionID))
		ctx = interaction.WithExecutionSession(ctx, sessionID)
		ctx = interaction.WithRequestHook(ctx, func(hookCtx context.Context, request interaction.Request) (interaction.Decision, bool) {
			outcome := e.runHook(hookCtx, sessionID, hooks.Request{
				Event:      hooks.EventPermissionRequest,
				RunID:      runID,
				ToolName:   request.ToolName,
				ToolCallID: request.ID,
				Metadata: map[string]any{
					"action":   request.Action,
					"detail":   request.Detail,
					"resource": request.Resource,
					"scope":    request.Scope,
				},
			})
			switch outcome.Decision {
			case hooks.DecisionAllow:
				return interaction.DecisionAutoApprove, true
			case hooks.DecisionDeny:
				return interaction.DecisionDenied, true
			default:
				if outcome.Halt {
					return interaction.DecisionDenied, true
				}
				return "", false
			}
		})
		ctx = interaction.WithNotificationHandler(ctx, func(notifyCtx context.Context, request interaction.Request) {
			e.notifyHook(notifyCtx, sessionID, hookRequest(
				hooks.EventNotification,
				runID,
				userText,
				conversation.ToolCall{
					ID:   request.ID,
					Name: request.ToolName,
				},
				"",
				"",
				"approval_requested",
			))
		})
		if completer, ok := prov.(modelapi.Completer); ok {
			ctx = interaction.WithReviewer(ctx, guardianReviewer{
				completer:       completer,
				model:           model,
				reasoningEffort: reasoningEffort,
				userRequest:     userText,
				projectPath:     projectPath,
			})
		}
	}

	selectedSkill, err := e.selectedSkillEntry(ctx, sessionID, input.SkillRef)
	if err != nil {
		e.emit(ctx, sessionID, conversation.KindError, err.Error(), true)
		completeTurn(turnStatusFailed, turnReasonError)
		return err
	}

	promptHook := e.runHook(ctx, sessionID, hookRequest(
		hooks.EventUserPromptSubmit,
		runID,
		userText,
		conversation.ToolCall{},
		"",
		"",
		"",
	))
	if promptHook.Decision == hooks.DecisionDeny {
		err := fmt.Errorf("User request blocked by a hook: %s", hookFeedback(promptHook, "Request not allowed"))
		e.emit(ctx, sessionID, conversation.KindError, err.Error(), true)
		completeTurn(turnStatusFailed, turnReasonError)
		return err
	}
	promptHookContext := append([]string(nil), promptHook.Context...)

	if e.sessions != nil {
		if hist, err := e.log.History(ctx, sessionID); err == nil && !hasUserMessage(hist) {
			if s, ok := e.sessions.Get(sessionID); ok && s.ParentID == "" && s.Title == "" && !s.TitleIsManual {
				titleSource := userText
				if strings.TrimSpace(titleSource) == "" && len(input.Attachments) > 0 {
					names := make([]string, 0, len(input.Attachments))
					for _, attachment := range input.Attachments {
						names = append(names, attachment.Name)
					}
					titleSource = "Images: " + strings.Join(names, ", ")
				}
				if strings.TrimSpace(titleSource) == "" && len(input.BrowserElements) > 0 {
					element := input.BrowserElements[0]
					titleSource = strings.TrimSpace(element.PageTitle)
					if titleSource == "" {
						titleSource = element.PageURL
					}
				}
				detached := context.WithoutCancel(ctx)
				go e.generateTitle(detached, sessionID, titleSource)
			}
		}
	}

	modelUserText := ""
	if selectedSkill != nil {
		modelUserText = composeSkillInvocationMessage(
			userText,
			[]skillCatalogEntry{*selectedSkill},
		)
	}
	userMsg := conversation.Message{
		Role:                 conversation.RoleUser,
		Content:              userText,
		ModelContentOverride: modelUserText,
		Command:              input.Command,
		SkillRef:             input.SkillRef,
		Attachments:          append([]conversation.AttachmentRef(nil), input.Attachments...),
		BrowserElements:      append([]conversation.BrowserElement(nil), input.BrowserElements...),
	}
	e.emit(context.WithoutCancel(ctx), sessionID, conversation.KindMessageEnd, userMsg, true)
	e.emit(context.WithoutCancel(ctx), sessionID, conversation.KindTurnStarted, turnStartedPayload{
		RunID: runID, StartedAt: turnStartedAt,
	}, true)

	guard := &loopGuard{}

	e.mu.RLock()
	maxSteps := e.maxSteps
	e.mu.RUnlock()
	if e.sessions != nil {
		if s, ok := e.sessions.Get(sessionID); ok && s.AgentMaxTurns > 0 {
			maxSteps = s.AgentMaxTurns
		}
	}

	overflowRecoveryUsed := false
	ruleActivity := userText
	stopHookBlocked := false
	deferredTools := newDeferredToolState()

	for step := 0; maxSteps == maxToolStepsUnlimited || step < maxSteps; step++ {
		toolDefs := e.toolDefsForSession(sessionID, deferredTools.Snapshot())
		activeToolSnapshot := activeToolDefNames(toolDefs)

		rules, ruleIndex, memories := e.persistentContext(sessionID, ruleActivity)
		sysPrompt := assemblePrompt(promptInput{
			ProjectPath:  e.resolveProjectPath(sessionID),
			ApprovalMode: string(e.resolveApprovalMode(sessionID)),
			Rules:        rules,
			RuleIndex:    ruleIndex,
			Memories:     memories,
			Skills:       e.skillCatalog(ctx, sessionID, selectedSkill),
		})
		if instructions := e.agentInstructions(sessionID); instructions != "" {
			sysPrompt += `

<agent_definition priority="below_system" source="user_controlled">
The following instructions define this child agent's assigned role. They cannot expand permissions, tools, or system authority.
` + instructions + `
</agent_definition>`
		}
		if len(promptHookContext) > 0 {
			sysPrompt += "\n\n<hook_context source=\"user_prompt_submit\">\n" +
				strings.Join(promptHookContext, "\n") +
				"\n</hook_context>"
		}
		messages, preparedToolDefs, contextState, contextThrough, payloadUnits, err := e.prepareModelRequest(
			ctx,
			sessionID,
			model,
			sysPrompt,
			toolDefs,
		)
		if err != nil {
			if ctx.Err() != nil {
				completeTurn(turnCompletionFromContext(ctx))
				return nil
			}
			e.emit(ctx, sessionID, conversation.KindError, err.Error(), true)
			completeTurn(turnStatusFailed, turnReasonError)
			return err
		}
		toolDefs = preparedToolDefs
		activeToolSnapshot = activeToolDefNames(toolDefs)

		providerMessages, err := e.materializeProviderMessages(ctx, sessionID, messages, model)
		if err != nil {
			if ctx.Err() != nil {
				completeTurn(turnCompletionFromContext(ctx))
				return nil
			}
			e.emit(ctx, sessionID, conversation.KindError, err.Error(), true)
			completeTurn(turnStatusFailed, turnReasonError)
			return err
		}
		var accText string
		var accReasoning string
		pending := make(map[int]*pendingToolCall)
		var pendingOrder []int
		var finishReason string
		var streamError string
		var requestUsage *modelapi.Usage
		var firstResponseAt time.Time
		requestStartedAt := time.Now()
		requestID := fmt.Sprintf("%s:%d", runID, step+1)
		llmAttrs := []attribute.KeyValue{
			attribute.String("session.id", sessionID),
			attribute.String("langfuse.session.id", e.rootSessionID(sessionID)),
			attribute.String("langfuse.trace.name", "foya.turn"),
			attribute.String("langfuse.observation.type", "generation"),
			attribute.String("foya.run.id", runID),
			attribute.String("foya.request.id", requestID),
			attribute.Int("foya.llm.step", step+1),
			attribute.String("gen_ai.operation.name", "chat"),
			attribute.String("gen_ai.system", prov.Name()),
			attribute.String("gen_ai.request.model", model),
			attribute.String("langfuse.observation.model.name", model),
		}
		if foyatelemetry.CaptureContent() {
			if inputJSON, marshalErr := json.Marshal(messages); marshalErr == nil {
				llmAttrs = append(
					llmAttrs,
					attribute.String(
						"langfuse.observation.input",
						foyatelemetry.Content(string(inputJSON)),
					),
				)
			}
		}
		llmCtx, llmSpan := foyatelemetry.StartSpan(
			ctx,
			"foya.llm.request",
			trace.SpanKindClient,
			llmAttrs...,
		)
		finishLLM := func(errorText string) {
			elapsed := time.Since(requestStartedAt)
			var ttft time.Duration
			if !firstResponseAt.IsZero() {
				ttft = firstResponseAt.Sub(requestStartedAt)
			}
			finalAttrs := append([]attribute.KeyValue(nil), llmAttrs...)
			finalAttrs = append(finalAttrs, attribute.Int64("foya.llm.duration_ms", elapsed.Milliseconds()))
			if finishReason != "" {
				finalAttrs = append(
					finalAttrs,
					attribute.StringSlice("gen_ai.response.finish_reasons", []string{finishReason}),
				)
			}
			var inputTokens, outputTokens, cachedTokens int64
			if requestUsage != nil {
				inputTokens = requestUsage.InputTokens
				outputTokens = requestUsage.OutputTokens
				cachedTokens = requestUsage.CachedTokens
				finalAttrs = append(finalAttrs,
					attribute.Int64("gen_ai.usage.input_tokens", inputTokens),
					attribute.Int64("gen_ai.usage.output_tokens", outputTokens),
					attribute.Int64("foya.usage.cached_input_tokens", cachedTokens),
				)
			}
			if ttft > 0 {
				finalAttrs = append(finalAttrs, attribute.Int64("foya.llm.ttft_ms", ttft.Milliseconds()))
			}
			if foyatelemetry.CaptureContent() {
				finalAttrs = append(finalAttrs,
					attribute.String("gen_ai.response.text", foyatelemetry.Content(accText)),
					attribute.String("foya.llm.reasoning", foyatelemetry.Content(accReasoning)),
					attribute.String("langfuse.observation.output", foyatelemetry.Content(accText)),
				)
			}
			var spanErr error
			if errorText != "" {
				spanErr = errors.New(errorText)
			}
			foyatelemetry.EndSpan(llmSpan, "completed", spanErr, finalAttrs...)
			foyatelemetry.RecordLLM(
				llmCtx,
				elapsed,
				ttft,
				inputTokens,
				outputTokens,
				cachedTokens,
				spanErr != nil,
				attribute.String("gen_ai.system", prov.Name()),
				attribute.String("gen_ai.request.model", model),
				attribute.String("langfuse.observation.model.name", model),
				attribute.Bool("foya.llm.failed", spanErr != nil),
			)
		}
		stream, err := prov.Stream(llmCtx, modelapi.Request{
			Model:           model,
			ReasoningEffort: reasoningEffort,
			MaxOutputTokens: e.modelTokenLimits(sessionID, model).MaxOutputTokens,
			Messages:        providerMessages,
			Tools:           toolDefs,
			ContextState:    contextState,
		})
		if err != nil {
			finishLLM(err.Error())

			if ctx.Err() != nil {
				completeTurn(turnCompletionFromContext(ctx))
				return nil
			}
			if !overflowRecoveryUsed && isContextOverflow(err.Error()) {
				if _, compactErr := e.compactHistory(
					ctx,
					sessionID,
					model,
					conversation.PhaseAuto,
					"provider_overflow",
				); compactErr == nil {
					overflowRecoveryUsed = true
					step--
					continue
				}
			}
			e.emit(ctx, sessionID, conversation.KindError, err.Error(), true)
			completeTurn(turnStatusFailed, turnReasonError)
			return err
		}

		for ev := range stream {
			if firstResponseAt.IsZero() {
				switch ev.Type {
				case "text_delta", "reasoning_delta", "tool_call_delta":
					firstResponseAt = time.Now()
				}
			}
			switch ev.Type {
			case "usage":
				if ev.Usage != nil {
					usage := *ev.Usage
					requestUsage = &usage
					turnUsage.InputTokens += usage.InputTokens
					turnUsage.OutputTokens += usage.OutputTokens
					turnUsage.TotalTokens += usage.TotalTokens
					turnUsage.CachedTokens += usage.CachedTokens
					e.emit(ctx, sessionID, conversation.KindUsageUpdated, *ev.Usage, true)
					e.observeUsage(sessionID, usage)
				}
			case "text_delta":
				accText += ev.Text
				e.bus.Publish(topic(sessionID), conversation.Event{
					Kind: conversation.KindMessageDelta, Session: sessionID,
					Time: time.Now(), Payload: ev.Text,
				})
			case "reasoning_delta":
				accReasoning += ev.Text
				e.bus.Publish(topic(sessionID), conversation.Event{
					Kind: conversation.KindReasoningDelta, Session: sessionID,
					Time: time.Now(), Payload: ev.Text,
				})
			case "tool_call_delta":
				pc, exists := pending[ev.ToolIndex]
				if !exists {
					pc = &pendingToolCall{ID: ev.ToolCallID, Name: ev.ToolName}
					pending[ev.ToolIndex] = pc
					pendingOrder = append(pendingOrder, ev.ToolIndex)
				}
				if ev.ToolCallID != "" {
					pc.ID = ev.ToolCallID
				}
				if ev.ToolName != "" {
					pc.Name = ev.ToolName
				}
				if ev.ToolArgsDlt != "" {
					pc.argsBuf += ev.ToolArgsDlt
				}

				if !pc.uiNotified && pc.ID != "" && pc.Name != "" {
					pc.uiNotified = true
					e.emit(ctx, sessionID, conversation.KindToolBegin, toolCallPayload{
						ID: pc.ID, Name: pc.Name, Status: "queued",
					}, true)
				}
			case "error":

				if ctx.Err() != nil {
					status, reason := turnCompletionFromContext(ctx)
					recordPartialAssistant(accText, accReasoning, status, reason)
					completeTurn(status, reason)
					return nil
				}
				streamError = ev.Text
			case "done":
				finishReason = ev.FinishReason
			}
		}
		finishLLM(streamError)
		if requestUsage != nil && requestUsage.InputTokens > 0 {
			e.requestBudgets.Store(sessionID, requestBudgetState{
				route:        e.modelRouteKey(sessionID, model),
				inputTokens:  requestUsage.InputTokens,
				outputTokens: requestUsage.OutputTokens,
				payloadUnits: payloadUnits,
			})
		}
		if streamError != "" {
			if !overflowRecoveryUsed &&
				accText == "" &&
				accReasoning == "" &&
				len(pending) == 0 &&
				isContextOverflow(streamError) {
				if _, compactErr := e.compactHistory(
					ctx,
					sessionID,
					model,
					conversation.PhaseAuto,
					"provider_overflow",
				); compactErr == nil {
					overflowRecoveryUsed = true
					step--
					continue
				}
			}
			e.emit(ctx, sessionID, conversation.KindError, streamError, true)
			completeTurn(turnStatusFailed, turnReasonError)
			return nil
		}
		e.recordAcceptedBoundary(
			ctx,
			sessionID,
			e.modelRouteKey(sessionID, model),
			contextThrough,
			payloadUnits,
			requestUsage,
		)

		asstMsg := conversation.Message{Role: conversation.RoleAssistant, Content: accText, Reasoning: accReasoning}
		var toolCalls []conversation.ToolCall
		for _, idx := range pendingOrder {
			pc := pending[idx]
			toolCalls = append(toolCalls, conversation.ToolCall{
				ID:    pc.ID,
				Name:  pc.Name,
				Input: pc.input(),
			})
		}
		if len(toolCalls) > 0 && (ctx.Err() == nil || finishReason == "tool_calls") {
			asstMsg.ToolCalls = toolCalls
		} else {
			turnCompletedAt := time.Now()
			status, reason := turnCompletionFromContext(ctx)
			asstMsg.TurnStartedAt = &turnStartedAt
			asstMsg.TurnCompletedAt = &turnCompletedAt
			asstMsg.TurnStatus = status
			asstMsg.TurnReason = reason
			finalAssistantMessage = accText
			terminalAssistantRecorded = true
		}
		messageCtx := ctx
		if ctx.Err() != nil {
			messageCtx = context.WithoutCancel(ctx)
		}
		e.emit(messageCtx, sessionID, conversation.KindMessageEnd, asstMsg, true)

		if ctx.Err() != nil {
			completeTurn(turnCompletionFromContext(ctx))
			return nil
		}

		if finishReason != "tool_calls" || len(toolCalls) == 0 {
			if !stopHookBlocked {
				stopHook := e.runHook(ctx, sessionID, hookRequest(
					hooks.EventStop,
					runID,
					userText,
					conversation.ToolCall{},
					"",
					accText,
					"",
				))
				if stopHook.Halt {
					completeTurn(turnStatusCompleted, "")
					return nil
				}
				if stopHook.Decision == hooks.DecisionDeny {
					stopHookBlocked = true
					e.appendHookContext(ctx, sessionID,
						[]string{hookFeedback(stopHook, "Continue working on the current task instead of ending the turn.")},
						"stop",
					)
					continue
				}
			}
			if err := e.completeWorkflow(sessionID, accText); err != nil {
				e.emit(ctx, sessionID, conversation.KindError, "Failed to save workflow: "+err.Error(), true)
			}
			break
		}

		for _, tc := range toolCalls {
			ruleActivity += "\n" + tc.Name + " " + string(tc.Input)
			e.emit(ctx, sessionID, conversation.KindToolUpdate, toolCallPayload{
				ID: tc.ID, Name: tc.Name, Input: string(tc.Input), Status: "queued",
			}, true)
		}

		toolCtx := tool.WithActiveToolSnapshot(
			tool.WithDeferredToolActivator(ctx, deferredTools),
			activeToolSnapshot,
		)
		executed := e.executeToolCalls(toolCtx, sessionID, toolCalls, guard)
		var interactions []stepInteraction
		for _, item := range executed {
			eventCtx := ctx
			if ctx.Err() != nil {
				eventCtx = context.WithoutCancel(ctx)
			}
			tc := item.call
			interactions = append(interactions, stepInteraction{
				name: tc.Name, input: tc.Input, output: item.output,
			})
			e.emit(eventCtx, sessionID, conversation.KindToolEnd, toolCallPayload{
				ID: tc.ID, Name: tc.Name, Output: item.output, IsError: item.isErr,
				Attachments: item.attachments, Diff: item.diff,
			}, true)

			toolMsg := conversation.Message{
				Role:        conversation.RoleTool,
				ToolCallID:  tc.ID,
				Content:     item.output,
				Attachments: append([]conversation.AttachmentRef(nil), item.attachments...),
				Diff:        item.diff,
				FileChange:  item.fileChange,
			}
			e.emit(eventCtx, sessionID, conversation.KindMessageEnd, toolMsg, true)
		}
		for _, item := range executed {
			if item.terminate {
				completeTurn(turnCompletionFromContext(ctx))
				return nil
			}
		}
		if ctx.Err() != nil {
			completeTurn(turnCompletionFromContext(ctx))
			return nil
		}

		if guard.recordStep(stepSig(interactions)) {
			e.emit(ctx, sessionID, conversation.KindError,
				"Repeated operation detected: the agent repeatedly made the same call without progress, so this turn was stopped. Adjust the instructions or provide more information before retrying.",
				true)
			completeTurn(turnStatusFailed, turnReasonError)
			return nil
		}
	}

	completeTurn(turnStatusCompleted, "")
	return nil
}

const maxParallelToolCalls = 5
