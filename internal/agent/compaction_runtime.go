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

	"github.com/freesoulcode/foya/internal/compaction"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/provider"
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
	Trigger         string                 `json:"trigger"`
	Phase           compaction.Phase       `json:"phase,omitempty"`
	Stage           string                 `json:"stage"`
	Outcome         string                 `json:"outcome"`
	Reason          string                 `json:"reason,omitempty"`
	ThroughSeq      event.Seq              `json:"through_seq,omitempty"`
	CoveredMessages int                    `json:"covered_messages,omitempty"`
	CheckpointID    string                 `json:"checkpoint_id,omitempty"`
	EstimatedBefore int64                  `json:"estimated_tokens_before,omitempty"`
	EstimatedAfter  int64                  `json:"estimated_tokens_after,omitempty"`
	Pressure        compaction.Pressure    `json:"pressure,omitempty"`
	Layers          *compaction.LayerUsage `json:"layers,omitempty"`
}

type generatedCheckpointProjection struct {
	kind              compaction.ProjectionKind
	summary           string
	segments          []compaction.Segment
	level             compaction.CheckpointLevel
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

func compactionFingerprint(plan compaction.Plan, route string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf(
		"%s\x00%s\x00%s\x00%s\x00%d\x00%d",
		compaction.PromptVersion,
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
		compaction.IsContextOverflow(err.Error())
}

func compactionInput(plan compaction.Plan) []message.Message {
	input := []message.Message{{
		Role:    message.RoleSystem,
		Content: compactionSystemPrompt,
	}}
	if plan.PreviousSummary != "" {
		input = append(input, message.Message{
			Role: message.RoleSystem,
			Content: "<previous_checkpoint>\n" +
				plan.PreviousSummary +
				"\n</previous_checkpoint>",
		})
	}
	input = append(input, plan.SourceMessages...)
	return append(input, message.Message{
		Role:    message.RoleUser,
		Content: "Produce the continuation checkpoint now.",
	})
}

func (e *Engine) generateCheckpointProjection(
	ctx context.Context,
	sessionID, model string,
	prov provider.Provider,
	generator compaction.Compactor,
	previous *compaction.Checkpoint,
	plan compaction.Plan,
	route string,
) (generatedCheckpointProjection, error) {
	if generator.Kind() == compaction.ProjectionProviderNative {
		request := provider.Request{
			Model:           model,
			ReasoningEffort: e.resolveReasoningEffort(sessionID),
			Messages:        provider.TextMessages(compactionInput(plan)),
		}
		if previous != nil &&
			previous.ProjectionKind == compaction.ProjectionProviderNative &&
			previous.ProviderRoute == route {
			request.ContextState = &provider.ContextState{
				Kind: previous.ProviderStateKind,
				Data: append(json.RawMessage(nil), previous.ProviderState...),
			}
		}
		candidate, err := generator.Compact(ctx, prov, request)
		if err != nil {
			return generatedCheckpointProjection{}, err
		}
		e.recordCompactionUsage(ctx, sessionID, model, candidate.Usage)
		if candidate.Kind != compaction.ProjectionProviderNative ||
			candidate.ProviderStateKind == "" ||
			len(candidate.ProviderState) == 0 ||
			!json.Valid(candidate.ProviderState) {
			return generatedCheckpointProjection{}, fmt.Errorf(
				"%w: provider-native state is invalid",
				errCompactionInvalidSummary,
			)
		}
		return generatedCheckpointProjection{
			kind:              compaction.ProjectionProviderNative,
			providerRoute:     route,
			providerStateKind: candidate.ProviderStateKind,
			providerState:     append(json.RawMessage(nil), candidate.ProviderState...),
		}, nil
	}

	generationPlan := plan
	appendSegment := compaction.CanAppendSegment(previous, plan)
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
		segments := compaction.AppendSegment(previous, plan, generated)
		return generatedCheckpointProjection{
			kind:     compaction.ProjectionText,
			summary:  compaction.RenderSegments(segments),
			segments: segments,
			level:    compaction.CheckpointLevelSegmented,
		}, nil
	}
	level := compaction.CheckpointLevelSegmented
	if previous != nil {
		level = compaction.CheckpointLevelSession
	}
	return generatedCheckpointProjection{
		kind:     compaction.ProjectionText,
		summary:  generated,
		segments: compaction.ConsolidatedSegment(plan, generated),
		level:    level,
	}, nil
}

func (e *Engine) recordCompactionUsage(
	ctx context.Context,
	sessionID, model string,
	usage *provider.Usage,
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
		event.KindUsageUpdated,
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
		event.KindCompactionDiagnostic,
		diagnostic,
		true,
	)
}

func (e *Engine) acceptedBoundaryForRoute(
	ctx context.Context,
	sessionID, route string,
) (compaction.AcceptedBoundary, bool, error) {
	cacheKey := acceptedBoundaryCacheKey(sessionID, route)
	if value, ok := e.acceptedBoundaries.Load(cacheKey); ok {
		boundary := value.(compaction.AcceptedBoundary)
		return boundary, true, nil
	}
	boundary, ok, err := e.log.AcceptedBoundary(ctx, sessionID, route)
	if err != nil || !ok {
		return compaction.AcceptedBoundary{}, false, err
	}
	e.acceptedBoundaries.Store(cacheKey, boundary)
	return boundary, true, nil
}

func (e *Engine) recordAcceptedBoundary(
	ctx context.Context,
	sessionID, route string,
	throughSeq event.Seq,
	payloadUnits int64,
	usage *provider.Usage,
) {
	if throughSeq == 0 {
		return
	}
	boundary := compaction.AcceptedBoundary{
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
		event.KindContextRequestAccepted,
		boundary,
		true,
	)
}

func acceptedBoundaryCacheKey(sessionID, route string) string {
	return sessionID + "\x00" + route
}
