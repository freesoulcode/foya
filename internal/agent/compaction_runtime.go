package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	conversation "github.com/freesoulcode/foya/internal/conversation"

	modelapi "github.com/freesoulcode/foya/internal/model"
)

const (
	maxCompactionFailureHashes = 16
)

var (
	errCompactionInvalidSummary = errors.New("invalid compaction summary")
	errCompactionNoSavings      = errors.New("compaction produced no savings")
	errCompactionSuppressed     = errors.New("compaction suppressed after deterministic failure")
)

type compactionFailureCircuit struct {
	mu    sync.Mutex
	order []string
	seen  map[string]struct{}
}

type compactionDiagnostic struct {
	Trigger         string                       `json:"trigger"`
	Phase           conversation.CompactionPhase `json:"phase,omitempty"`
	Stage           string                       `json:"stage"`
	Outcome         string                       `json:"outcome"`
	Reason          string                       `json:"reason,omitempty"`
	ThroughSeq      conversation.Seq             `json:"through_seq,omitempty"`
	CoveredMessages int                          `json:"covered_messages,omitempty"`
	CheckpointID    string                       `json:"checkpoint_id,omitempty"`
	EstimatedBefore int64                        `json:"estimated_tokens_before,omitempty"`
	EstimatedAfter  int64                        `json:"estimated_tokens_after,omitempty"`
	Pressure        conversation.Pressure        `json:"pressure,omitempty"`
	Layers          *conversation.LayerUsage     `json:"layers,omitempty"`
}

type generatedCheckpointProjection struct {
	kind              conversation.ProjectionKind
	summary           string
	segments          []conversation.Segment
	level             conversation.CheckpointLevel
	providerRoute     string
	providerStateKind string
	providerState     json.RawMessage
}

func (e *Engine) compactionFailureSeen(sessionID, fingerprint string) bool {
	value, ok := e.compactionFailures.Load(sessionID)
	if !ok {
		return false
	}
	circuit := value.(*compactionFailureCircuit)
	circuit.mu.Lock()
	defer circuit.mu.Unlock()
	_, ok = circuit.seen[fingerprint]
	return ok
}

func (e *Engine) rememberCompactionFailure(sessionID, fingerprint string) {
	value, _ := e.compactionFailures.LoadOrStore(
		sessionID,
		&compactionFailureCircuit{seen: make(map[string]struct{})},
	)
	circuit := value.(*compactionFailureCircuit)
	circuit.mu.Lock()
	defer circuit.mu.Unlock()
	if _, ok := circuit.seen[fingerprint]; ok {
		return
	}
	circuit.seen[fingerprint] = struct{}{}
	circuit.order = append(circuit.order, fingerprint)
	if len(circuit.order) <= maxCompactionFailureHashes {
		return
	}
	oldest := circuit.order[0]
	circuit.order = circuit.order[1:]
	delete(circuit.seen, oldest)
}

func (e *Engine) clearCompactionFailures(sessionID string) {
	e.compactionFailures.Delete(sessionID)
}

func compactionFingerprint(plan conversation.Plan, route string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf(
		"%s\x00%s\x00%s\x00%s\x00%d\x00%d",
		conversation.PromptVersion,
		route,
		plan.PreviousCheckpointID,
		plan.SourceDigest,
		plan.ThroughSeq,
		plan.HeadAnchorSeq,
	)))
	return hex.EncodeToString(sum[:])
}

func deterministicCompactionFailure(err error) bool {
	return errors.Is(err, errCompactionInvalidSummary) ||
		errors.Is(err, errCompactionNoSavings) ||
		conversation.IsContextOverflow(err.Error())
}

func compactionInput(plan conversation.Plan) []conversation.Message {
	input := []conversation.Message{{
		Role:    conversation.RoleSystem,
		Content: compactionSystemPrompt,
	}}
	if plan.PreviousSummary != "" {
		input = append(input, conversation.Message{
			Role: conversation.RoleSystem,
			Content: "<previous_checkpoint>\n" +
				plan.PreviousSummary +
				"\n</previous_checkpoint>",
		})
	}
	input = append(input, plan.SourceMessages...)
	return append(input, conversation.Message{
		Role:    conversation.RoleUser,
		Content: "Produce the continuation checkpoint now.",
	})
}

func (e *Engine) generateCheckpointProjection(
	ctx context.Context,
	sessionID, model string,
	prov modelapi.Provider,
	generator conversation.Compactor,
	previous *conversation.Checkpoint,
	plan conversation.Plan,
	route string,
) (generatedCheckpointProjection, error) {
	if generator.Kind() == conversation.ProjectionProviderNative {
		request := modelapi.Request{
			Model:           model,
			ReasoningEffort: e.resolveReasoningEffort(sessionID),
			Messages:        modelMessages(compactionInput(plan)),
		}
		if previous != nil &&
			previous.ProjectionKind == conversation.ProjectionProviderNative &&
			previous.ProviderRoute == route {
			request.ContextState = &modelapi.ContextState{
				Kind: previous.ProviderStateKind,
				Data: append(json.RawMessage(nil), previous.ProviderState...),
			}
		}
		candidate, err := generator.Compact(ctx, prov, request)
		if err != nil {
			return generatedCheckpointProjection{}, err
		}
		e.recordCompactionUsage(ctx, sessionID, model, candidate.Usage)
		if candidate.Kind != conversation.ProjectionProviderNative ||
			candidate.ProviderStateKind == "" ||
			len(candidate.ProviderState) == 0 ||
			!json.Valid(candidate.ProviderState) {
			return generatedCheckpointProjection{}, fmt.Errorf(
				"%w: provider-native state is invalid",
				errCompactionInvalidSummary,
			)
		}
		return generatedCheckpointProjection{
			kind:              conversation.ProjectionProviderNative,
			providerRoute:     route,
			providerStateKind: candidate.ProviderStateKind,
			providerState:     append(json.RawMessage(nil), candidate.ProviderState...),
		}, nil
	}

	generationPlan := plan
	appendSegment := conversation.CanAppendSegment(previous, plan)
	if appendSegment {
		generationPlan.PreviousSummary = ""
	}
	generated, err := e.generateCompactionSummary(
		ctx,
		sessionID,
		model,
		prov,
		generator,
		compactionInput(generationPlan),
	)
	if err != nil {
		return generatedCheckpointProjection{}, err
	}
	if appendSegment {
		segments := conversation.AppendSegment(previous, plan, generated)
		return generatedCheckpointProjection{
			kind:     conversation.ProjectionText,
			summary:  conversation.RenderSegments(segments),
			segments: segments,
			level:    conversation.CheckpointLevelSegmented,
		}, nil
	}
	level := conversation.CheckpointLevelSegmented
	if previous != nil {
		level = conversation.CheckpointLevelSession
	}
	return generatedCheckpointProjection{
		kind:     conversation.ProjectionText,
		summary:  generated,
		segments: conversation.ConsolidatedSegment(plan, generated),
		level:    level,
	}, nil
}

func (e *Engine) recordCompactionUsage(
	ctx context.Context,
	sessionID, model string,
	usage *modelapi.Usage,
) {
	if usage == nil {
		return
	}
	value := *usage
	if value.Model == "" {
		value.Model = model
	}
	e.emit(
		context.WithoutCancel(ctx),
		sessionID,
		conversation.KindUsageUpdated,
		value,
		true,
	)
	e.observeUsage(sessionID, value)
}

func (e *Engine) emitCompactionDiagnostic(
	ctx context.Context,
	sessionID string,
	diagnostic compactionDiagnostic,
) {
	e.emit(
		context.WithoutCancel(ctx),
		sessionID,
		conversation.KindCompactionDiagnostic,
		diagnostic,
		true,
	)
}

func (e *Engine) acceptedBoundaryForRoute(
	ctx context.Context,
	sessionID, route string,
) (conversation.AcceptedBoundary, bool, error) {
	cacheKey := acceptedBoundaryCacheKey(sessionID, route)
	if value, ok := e.acceptedBoundaries.Load(cacheKey); ok {
		boundary := value.(conversation.AcceptedBoundary)
		return boundary, true, nil
	}
	boundary, ok, err := e.log.AcceptedBoundary(ctx, sessionID, route)
	if err != nil || !ok {
		return conversation.AcceptedBoundary{}, false, err
	}
	e.acceptedBoundaries.Store(cacheKey, boundary)
	return boundary, true, nil
}

func (e *Engine) recordAcceptedBoundary(
	ctx context.Context,
	sessionID, route string,
	throughSeq conversation.Seq,
	payloadUnits int64,
	usage *modelapi.Usage,
) {
	if throughSeq == 0 {
		return
	}
	boundary := conversation.AcceptedBoundary{
		SessionID:    sessionID,
		Route:        route,
		ThroughSeq:   throughSeq,
		PayloadUnits: payloadUnits,
		CreatedAt:    time.Now(),
	}
	if usage != nil {
		boundary.InputTokens = usage.InputTokens
		boundary.OutputTokens = usage.OutputTokens
	}
	e.acceptedBoundaries.Store(acceptedBoundaryCacheKey(sessionID, route), boundary)
	e.emit(
		context.WithoutCancel(ctx),
		sessionID,
		conversation.KindContextRequestAccepted,
		boundary,
		true,
	)
}

func acceptedBoundaryCacheKey(sessionID, route string) string {
	return sessionID + "\x00" + route
}
