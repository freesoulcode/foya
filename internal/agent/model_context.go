package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	conversation "github.com/freesoulcode/foya/internal/conversation"
	"github.com/freesoulcode/foya/internal/hooks"

	modelapi "github.com/freesoulcode/foya/internal/model"

	foyatelemetry "github.com/freesoulcode/foya/internal/telemetry"
	"github.com/freesoulcode/foya/internal/tool"
)

// prepareModelRequest materializes the current model-history projection.
// Budget estimates trigger compaction but never override the provider's final
// decision about whether a request fits.
func (e *Engine) prepareModelRequest(
	ctx context.Context,
	sessionID, model, systemPrompt string,
	tools []modelapi.ToolDef,
) ([]conversation.Message, []modelapi.ToolDef, *modelapi.ContextState, conversation.Seq, int64, error) {
	route := e.modelRouteKey(sessionID, model)
	projectionRoute := route
	if _, ok := e.currentProvider(sessionID).(modelapi.NativeContextCompactor); !ok {
		projectionRoute = ""
	}
	var prior requestBudgetState
	if previous, ok := e.requestBudgets.Load(sessionID); ok {
		candidate := previous.(requestBudgetState)
		if candidate.route == route {
			prior = candidate
		}
	}
	if prior.route == "" {
		if boundary, ok, _ := e.acceptedBoundaryForRoute(
			ctx,
			sessionID,
			route,
		); ok {
			prior = requestBudgetState{
				route:        route,
				inputTokens:  boundary.InputTokens,
				outputTokens: boundary.OutputTokens,
				payloadUnits: boundary.PayloadUnits,
			}
		}
	}
	contextWindow := e.contextWindow(ctx, sessionID, model)
	limits := e.modelTokenLimits(sessionID, model)
	compiler := conversation.ContextCompiler{}
	build := func() (conversation.CompiledContext, error) {
		projection, err := e.log.ModelContext(ctx, sessionID, projectionRoute)
		if err != nil {
			return conversation.CompiledContext{}, err
		}
		history := projection.Messages
		if messagesContainToolResultReference(history) {
			tools = e.ensureHistoryResultToolDef(sessionID, tools)
		}
		messages := append([]conversation.Message{
			{Role: conversation.RoleSystem, Content: systemPrompt},
		}, history...)
		return compiler.Compile(conversation.CompileInput{
			Messages:          messages,
			Tools:             tools,
			ContextState:      projection.ContextState,
			ThroughSeq:        projection.ThroughSeq,
			ContextWindow:     contextWindow,
			MaxInputTokens:    limits.MaxInputTokens,
			PriorInputTokens:  prior.inputTokens,
			PriorOutputTokens: prior.outputTokens,
			PriorPayloadUnits: prior.payloadUnits,
		}), nil
	}

	compiled, err := build()
	if err != nil {
		return nil, nil, nil, 0, 0, err
	}
	if compiled.NeedsCompaction {
		layers := compiled.Layers
		e.emitCompactionDiagnostic(ctx, sessionID, compactionDiagnostic{
			Trigger:         "budget",
			Phase:           conversation.PhaseAuto,
			Stage:           "budget",
			Outcome:         "triggered",
			Reason:          string(compiled.Pressure),
			EstimatedBefore: compiled.EstimatedTokens,
			Layers:          &layers,
		})
		if _, compactErr := e.compactHistory(
			ctx,
			sessionID,
			model,
			conversation.PhaseAuto,
			"budget",
		); compactErr == nil {
			compiled, err = build()
			if err != nil {
				return nil, nil, nil, 0, 0, err
			}
		}
	}

	if compiled.NeedsCompaction {
		bounded, rewritten := conversation.BoundToolResultsWithPolicy(
			compiled.Messages,
			conversation.ToolResultPolicy{
				MaxTokens:  conversation.MaxToolResultTokens,
				KeepNewest: 1,
			},
		)
		if rewritten > 0 {
			tools = e.ensureHistoryResultToolDef(sessionID, tools)
			compiled = compiler.Compile(conversation.CompileInput{
				Messages:          bounded,
				Tools:             tools,
				ContextState:      compiled.ContextState,
				ThroughSeq:        compiled.ThroughSeq,
				ContextWindow:     contextWindow,
				MaxInputTokens:    limits.MaxInputTokens,
				PriorInputTokens:  prior.inputTokens,
				PriorOutputTokens: prior.outputTokens,
				PriorPayloadUnits: prior.payloadUnits,
			})
		}
	}
	if compiled.NeedsCompaction {
		bounded, rewritten := conversation.BoundToolResults(
			compiled.Messages,
			conversation.MaxToolResultTokens,
		)
		if rewritten > 0 {
			tools = e.ensureHistoryResultToolDef(sessionID, tools)
			compiled = compiler.Compile(conversation.CompileInput{
				Messages:          bounded,
				Tools:             tools,
				ContextState:      compiled.ContextState,
				ThroughSeq:        compiled.ThroughSeq,
				ContextWindow:     contextWindow,
				MaxInputTokens:    limits.MaxInputTokens,
				PriorInputTokens:  prior.inputTokens,
				PriorOutputTokens: prior.outputTokens,
				PriorPayloadUnits: prior.payloadUnits,
			})
		}
	}
	if compiled.NeedsCompaction {
		layers := compiled.Layers
		e.emitCompactionDiagnostic(ctx, sessionID, compactionDiagnostic{
			Trigger:         "budget",
			Phase:           conversation.PhaseAuto,
			Stage:           "dispatch",
			Outcome:         "provider_decides",
			Reason:          string(compiled.Pressure),
			EstimatedBefore: compiled.EstimatedTokens,
			Layers:          &layers,
		})
	}
	return compiled.Messages, compiled.Tools, compiled.ContextState,
		compiled.ThroughSeq, compiled.PayloadUnits, nil
}

func (e *Engine) materializeProviderMessages(
	ctx context.Context,
	sessionID string,
	messages []conversation.Message,
	model string,
) ([]modelapi.InputMessage, error) {
	e.mu.RLock()
	store := e.artifacts
	e.mu.RUnlock()
	prov := e.currentProvider(sessionID)
	imageInput := (*bool)(nil)
	e.mu.RLock()
	imageCapability := e.imageCapability
	e.mu.RUnlock()
	if imageCapability != nil {
		imageInput = imageCapability(sessionID, model)
	} else if resolver, ok := prov.(modelapi.CapabilityResolver); ok {
		imageInput = resolver.ModelCapabilities(model).ImageInput
	}

	out := make([]modelapi.InputMessage, 0, len(messages))
	var usedImageBytes int64
	for _, item := range messages {
		projected := modelMessage(item)
		for _, attachment := range item.Attachments {
			if attachment.Kind != "image" {
				projected.Parts = append(projected.Parts, modelapi.InputPart{
					Type: "text", Text: "[Attachment: " + attachment.Name + "]",
				})
				continue
			}
			if imageInput != nil && !*imageInput {
				projected.Parts = append(projected.Parts, modelapi.InputPart{
					Type: "text", Text: "[Image attachment omitted: selected model does not support image input]",
				})
				continue
			}
			if store == nil {
				projected.Parts = append(projected.Parts, modelapi.InputPart{
					Type: "text", Text: "[Image attachment unavailable: artifact store is not configured]",
				})
				continue
			}
			data, stored, err := store.Read(ctx, sessionID, attachment.ID)
			if err != nil {
				projected.Parts = append(projected.Parts, modelapi.InputPart{
					Type: "text", Text: "[Image attachment unavailable: " + attachment.Name + "]",
				})
				continue
			}
			if usedImageBytes+int64(len(data)) > maxProviderImageBytes {
				projected.Parts = append(projected.Parts, modelapi.InputPart{
					Type: "text", Text: "[Image attachment omitted: per-request image budget exceeded]",
				})
				continue
			}
			usedImageBytes += int64(len(data))
			projected.Parts = append(projected.Parts, modelapi.InputPart{
				Type: "image", Data: data, MediaType: stored.MediaType, Detail: "auto",
			})
		}
		out = append(out, projected)
	}
	return out, nil
}

// CompactSession performs a standalone/manual compaction while the session is idle.
func (e *Engine) CompactSession(
	ctx context.Context,
	sessionID string,
) (*conversation.Checkpoint, error) {
	compactCtx, cancel := context.WithCancelCause(ctx)
	runState := &turnRunState{cancel: cancel}
	if _, loaded := e.cancels.LoadOrStore(sessionID, runState); loaded {
		cancel(context.Canceled)
		return nil, fmt.Errorf("The chat is busy")
	}
	done := make(chan struct{})
	e.dones.Store(sessionID, done)
	defer e.cancels.Delete(sessionID)
	defer e.dones.Delete(sessionID)
	defer close(done)
	defer cancel(nil)

	e.setSessionPhase(compactCtx, sessionID, conversation.PhaseCompact)
	defer e.setSessionPhase(context.WithoutCancel(compactCtx), sessionID, conversation.PhaseIdle)
	return e.compactHistory(
		compactCtx,
		sessionID,
		e.currentModel(sessionID),
		conversation.PhaseStandalone,
		"manual",
	)
}

func (e *Engine) compactHistory(
	ctx context.Context,
	sessionID, model string,
	phase conversation.CompactionPhase,
	trigger string,
) (checkpointResult *conversation.Checkpoint, resultErr error) {
	events, err := e.log.Events(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	previous, _, err := e.log.Checkpoint(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	prov := e.currentProvider(sessionID)
	generators := e.availableCompactors(prov)
	if len(generators) == 0 {
		return nil, ErrCompactionUnavailable
	}
	generator := generators[0]
	route := e.modelRouteKey(sessionID, model)
	planningPrevious := previous
	if previous != nil &&
		previous.ProjectionKind == conversation.ProjectionProviderNative &&
		(previous.ProviderRoute != route ||
			generator.Kind() != conversation.ProjectionProviderNative) {
		planningPrevious = nil
	}
	plan, ok := conversation.BuildPlanForPhase(events, planningPrevious, phase)
	if !ok {
		e.emitCompactionDiagnostic(ctx, sessionID, compactionDiagnostic{
			Trigger: trigger,
			Phase:   phase,
			Stage:   "planning",
			Outcome: "skipped",
			Reason:  ErrNothingToCompact.Error(),
		})
		return nil, ErrNothingToCompact
	}
	if planningPrevious == nil && previous != nil {
		plan.PreviousCheckpointID = previous.CheckpointID
	}
	compactionStartedAt := time.Now()
	compactionAttrs := []attribute.KeyValue{
		attribute.String("session.id", sessionID),
		attribute.String("langfuse.session.id", e.rootSessionID(sessionID)),
		attribute.String("langfuse.trace.name", "foya.turn"),
		attribute.String("langfuse.observation.type", "span"),
		attribute.String("foya.run.id", tool.RunIDFromContext(ctx)),
		attribute.String("foya.compaction.trigger", trigger),
		attribute.String("foya.compaction.phase", string(plan.Phase)),
		attribute.Int64("foya.compaction.through_seq", int64(plan.ThroughSeq)),
		attribute.Int("foya.compaction.covered_messages", plan.CoveredMessages),
		attribute.Int64("foya.compaction.estimated_tokens_before", plan.EstimatedTokens),
	}
	ctx, compactionSpan := foyatelemetry.StartSpan(
		ctx,
		"foya.compaction",
		trace.SpanKindInternal,
		compactionAttrs...,
	)
	defer func() {
		finalAttrs := append([]attribute.KeyValue(nil), compactionAttrs...)
		status := "completed"
		if checkpointResult != nil {
			finalAttrs = append(finalAttrs,
				attribute.String("foya.compaction.checkpoint_id", checkpointResult.CheckpointID),
				attribute.Int64("foya.compaction.estimated_tokens_after", checkpointResult.EstimatedTokensAfter),
			)
		}
		if resultErr != nil {
			status = "failed"
		}
		foyatelemetry.EndSpan(compactionSpan, status, resultErr, finalAttrs...)
		foyatelemetry.RecordCompaction(
			context.WithoutCancel(ctx),
			time.Since(compactionStartedAt),
			attribute.String("foya.compaction.trigger", trigger),
			attribute.String("foya.compaction.phase", string(plan.Phase)),
			attribute.String("foya.compaction.status", status),
		)
	}()
	preCompact := e.runHook(ctx, sessionID, hooks.Request{
		Event:             hooks.EventPreCompact,
		RunID:             tool.RunIDFromContext(ctx),
		CompactionTrigger: trigger,
		CompactionPhase:   string(plan.Phase),
		Metadata: map[string]any{
			"through_seq":      plan.ThroughSeq,
			"covered_messages": plan.CoveredMessages,
			"estimated_tokens": plan.EstimatedTokens,
		},
	})
	if preCompact.Decision == hooks.DecisionDeny || preCompact.Halt {
		reason := hookFeedback(preCompact, ErrCompactionBlockedByHook.Error())
		return nil, fmt.Errorf("%w: %s", ErrCompactionBlockedByHook, reason)
	}
	defer func() {
		completedAt := time.Now()
		status := "completed"
		errorText := ""
		metadata := map[string]any{
			"trigger": trigger,
			"phase":   plan.Phase,
		}
		if checkpointResult != nil {
			metadata["checkpoint_id"] = checkpointResult.CheckpointID
			metadata["estimated_tokens_before"] = checkpointResult.EstimatedTokensBefore
			metadata["estimated_tokens_after"] = checkpointResult.EstimatedTokensAfter
		}
		if resultErr != nil {
			status = "failed"
			errorText = resultErr.Error()
		}
		e.observeHook(context.WithoutCancel(ctx), sessionID, hooks.Request{
			Event:             hooks.EventPostCompact,
			RunID:             tool.RunIDFromContext(ctx),
			CompactionTrigger: trigger,
			CompactionPhase:   string(plan.Phase),
			Status:            status,
			Error:             errorText,
			StartedAt:         &compactionStartedAt,
			CompletedAt:       &completedAt,
			DurationMS:        completedAt.Sub(compactionStartedAt).Milliseconds(),
			Metadata:          metadata,
		})
	}()
	fingerprint := compactionFingerprint(plan, route)
	if e.compactionFailureSeen(sessionID, fingerprint) {
		e.emitCompactionDiagnostic(ctx, sessionID, compactionDiagnostic{
			Trigger:         trigger,
			Phase:           plan.Phase,
			Stage:           "generation",
			Outcome:         "suppressed",
			Reason:          errCompactionSuppressed.Error(),
			ThroughSeq:      plan.ThroughSeq,
			CoveredMessages: plan.CoveredMessages,
			EstimatedBefore: plan.EstimatedTokens,
		})
		return nil, errCompactionSuppressed
	}

	e.emit(ctx, sessionID, conversation.KindCompactionStarted, map[string]any{
		"trigger":          trigger,
		"phase":            plan.Phase,
		"through_seq":      plan.ThroughSeq,
		"covered_messages": plan.CoveredMessages,
	}, true)

	projection, err := e.generateCheckpointProjection(
		ctx,
		sessionID,
		model,
		prov,
		generator,
		planningPrevious,
		plan,
		route,
	)
	if err != nil &&
		(errors.Is(err, modelapi.ErrNativeCompactionUnsupported) ||
			errors.Is(err, errCompactionInvalidSummary)) &&
		generator.Kind() == conversation.ProjectionProviderNative {
		for _, fallback := range generators[1:] {
			if fallback.Kind() != conversation.ProjectionText {
				continue
			}
			if previous != nil &&
				previous.ProjectionKind == conversation.ProjectionProviderNative {
				fullPlan, fullOK := conversation.BuildPlanForPhase(events, nil, phase)
				if !fullOK {
					break
				}
				fullPlan.PreviousCheckpointID = previous.CheckpointID
				plan = fullPlan
				planningPrevious = nil
			}
			generator = fallback
			fingerprint = compactionFingerprint(plan, route)
			projection, err = e.generateCheckpointProjection(
				ctx,
				sessionID,
				model,
				prov,
				generator,
				planningPrevious,
				plan,
				route,
			)
			break
		}
	}
	if err != nil && conversation.IsContextOverflow(err.Error()) {
		e.rememberCompactionFailure(sessionID, fingerprint)
		if accepted, found, _ := e.acceptedBoundaryForRoute(
			ctx,
			sessionID,
			route,
		); found {
			if accepted.ThroughSeq > 0 &&
				accepted.ThroughSeq < plan.ThroughSeq {
				retreated, retreatOK := conversation.BuildPlanWithOptions(
					events,
					planningPrevious,
					conversation.PlanOptions{
						Phase:         phase,
						MaxThroughSeq: accepted.ThroughSeq,
					},
				)
				if retreatOK && retreated.ThroughSeq < plan.ThroughSeq {
					if planningPrevious == nil && previous != nil {
						retreated.PreviousCheckpointID = previous.CheckpointID
					}
					plan = retreated
					fingerprint = compactionFingerprint(plan, route)
					if !e.compactionFailureSeen(sessionID, fingerprint) {
						projection, err = e.generateCheckpointProjection(
							ctx,
							sessionID,
							model,
							prov,
							generator,
							planningPrevious,
							plan,
							route,
						)
					} else {
						err = errCompactionSuppressed
					}
				}
			}
		}
	}
	if err != nil {
		if deterministicCompactionFailure(err) {
			e.rememberCompactionFailure(sessionID, fingerprint)
		}
		e.emitCompactionDiagnostic(ctx, sessionID, compactionDiagnostic{
			Trigger:         trigger,
			Phase:           plan.Phase,
			Stage:           "generation",
			Outcome:         "failed_open",
			Reason:          err.Error(),
			ThroughSeq:      plan.ThroughSeq,
			CoveredMessages: plan.CoveredMessages,
			EstimatedBefore: plan.EstimatedTokens,
		})
		e.emit(context.WithoutCancel(ctx), sessionID, conversation.KindCompactionFailed, err.Error(), true)
		return nil, err
	}
	summary := projection.summary
	var estimatedAfter int64
	if projection.kind == conversation.ProjectionProviderNative {
		estimatedAfter = conversation.EstimateTextTokens(string(projection.providerState))
	} else {
		estimatedAfter = conversation.EstimateCheckpointTokens(summary, plan.HeadAnchor)
	}
	if projection.kind == conversation.ProjectionText &&
		estimatedAfter >= plan.EstimatedTokens {
		err := fmt.Errorf(
			"%w: "+
				"before=%d after=%d",
			errCompactionNoSavings,
			plan.EstimatedTokens,
			estimatedAfter,
		)
		e.rememberCompactionFailure(sessionID, fingerprint)
		e.emitCompactionDiagnostic(ctx, sessionID, compactionDiagnostic{
			Trigger:         trigger,
			Phase:           plan.Phase,
			Stage:           "validation",
			Outcome:         "failed_open",
			Reason:          err.Error(),
			ThroughSeq:      plan.ThroughSeq,
			CoveredMessages: plan.CoveredMessages,
			EstimatedBefore: plan.EstimatedTokens,
			EstimatedAfter:  estimatedAfter,
		})
		e.emit(context.WithoutCancel(ctx), sessionID, conversation.KindCompactionFailed, err.Error(), true)
		return nil, err
	}

	checkpoint := conversation.Checkpoint{
		SchemaVersion:         conversation.CheckpointSchemaVersion,
		SourcePolicyVersion:   conversation.SourcePolicyVersion,
		SummaryFormatVersion:  conversation.SummaryFormatVersion,
		PromptVersion:         conversation.PromptVersion,
		PreviousCheckpointID:  plan.PreviousCheckpointID,
		SessionID:             sessionID,
		Phase:                 plan.Phase,
		HeadAnchorSeq:         plan.HeadAnchorSeq,
		ThroughSeq:            plan.ThroughSeq,
		SourceDigest:          plan.SourceDigest,
		ProjectionKind:        projection.kind,
		Level:                 projection.level,
		Segments:              projection.segments,
		Summary:               summary,
		ProviderRoute:         projection.providerRoute,
		ProviderStateKind:     projection.providerStateKind,
		ProviderState:         projection.providerState,
		Model:                 model,
		EstimatedRawTokens:    plan.EstimatedRawTokens,
		EstimatedTokensBefore: plan.EstimatedTokens,
		EstimatedTokensAfter:  estimatedAfter,
		CreatedAt:             time.Now(),
	}
	checkpoint.CheckpointID = conversation.ComputeCheckpointID(checkpoint)
	completedEvent, err := e.log.RecordCheckpoint(ctx, checkpoint)
	if err != nil {
		e.emitCompactionDiagnostic(ctx, sessionID, compactionDiagnostic{
			Trigger:         trigger,
			Phase:           plan.Phase,
			Stage:           "persistence",
			Outcome:         "failed_open",
			Reason:          err.Error(),
			ThroughSeq:      plan.ThroughSeq,
			CoveredMessages: plan.CoveredMessages,
			EstimatedBefore: plan.EstimatedTokens,
			EstimatedAfter:  estimatedAfter,
		})
		e.emit(context.WithoutCancel(ctx), sessionID, conversation.KindCompactionFailed, err.Error(), true)
		return nil, err
	}
	e.clearCompactionFailures(sessionID)
	e.emitCompactionDiagnostic(ctx, sessionID, compactionDiagnostic{
		Trigger:         trigger,
		Phase:           plan.Phase,
		Stage:           "complete",
		Outcome:         "compacted",
		ThroughSeq:      plan.ThroughSeq,
		CoveredMessages: plan.CoveredMessages,
		CheckpointID:    checkpoint.CheckpointID,
		EstimatedBefore: plan.EstimatedTokens,
		EstimatedAfter:  estimatedAfter,
	})
	_ = e.bus.PublishMustDeliver(ctx, topic(sessionID), completedEvent)
	return &checkpoint, nil
}

func (e *Engine) generateCompactionSummary(
	ctx context.Context,
	sessionID, model string,
	prov modelapi.Provider,
	generator conversation.Compactor,
	input []conversation.Message,
) (string, error) {
	outputLimit := conversation.OutputTokenLimit(
		e.modelTokenLimits(sessionID, model).MaxOutputTokens,
	)
	request := modelapi.Request{
		Model:           model,
		ReasoningEffort: e.resolveReasoningEffort(sessionID),
		MaxOutputTokens: outputLimit,
		Messages:        modelMessages(input),
	}

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			retryMessages := append([]modelapi.InputMessage(nil), request.Messages...)
			retryMessages = append(retryMessages, modelMessage(conversation.Message{
				Role: conversation.RoleUser,
				Content: "The previous checkpoint was invalid or truncated. " +
					"Return a shorter complete checkpoint using exactly the required sections.",
			}))
			request.Messages = retryMessages
		}

		candidate, err := generator.Compact(ctx, prov, request)
		if err != nil {
			return "", err
		}
		if candidate.Kind != conversation.ProjectionText {
			return "", fmt.Errorf(
				"unsupported compaction projection kind %q",
				candidate.Kind,
			)
		}
		e.recordCompactionUsage(ctx, sessionID, model, candidate.Usage)

		summary := strings.TrimSpace(candidate.Text)
		if strings.EqualFold(candidate.FinishReason, "length") {
			lastErr = fmt.Errorf("compaction output reached its token limit")
			continue
		}
		if candidate.FinishReason != "" &&
			!strings.EqualFold(candidate.FinishReason, "stop") {
			return "", fmt.Errorf(
				"%w: stopped with finish reason %q",
				errCompactionInvalidSummary,
				candidate.FinishReason,
			)
		}
		if err := conversation.ValidateSummary(summary); err != nil {
			lastErr = err
			continue
		}
		return summary, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("compaction did not produce a valid checkpoint")
	}
	return "", fmt.Errorf("%w: %v", errCompactionInvalidSummary, lastErr)
}

func isContextOverflow(text string) bool {
	return conversation.IsContextOverflow(text)
}

func (e *Engine) setSessionPhase(ctx context.Context, sessionID string, phase conversation.Phase) {
	s, err := e.sessions.SetPhase(sessionID, phase)
	if err != nil {
		return
	}
	snapshot := *s
	e.emit(ctx, sessionID, conversation.KindSessionUpdated, snapshot, true)
}
