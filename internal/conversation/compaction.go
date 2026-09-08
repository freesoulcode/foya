// Package compaction provides context-budget estimation and immutable history projections.
package conversation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	model "github.com/freesoulcode/foya/internal/model"
)

const (
	UnknownHistoryBudgetTokens int64 = 32_000
	UnknownReserveTokens       int64 = 16_384
	MaxReserveTokens           int64 = 16_384
	MaxToolResultTokens        int64 = 2_048
	DefaultOutputTokens        int64 = 4_096
	MaxOutputTokens            int64 = 8_192
	MinAdaptiveReserveTokens   int64 = 1_024
	MaxAdaptiveReserveTokens   int64 = 8_192

	CheckpointSchemaVersion = 3
	SourcePolicyVersion     = "message-prefix-v1"
	SummaryFormatVersion    = "continuation-v2"
	PromptVersion           = "continuation-prompt-v2"
)

const toolOutputOmission = "\n\n[... middle of tool output omitted ...]\n\n"

// Budget separates model input capacity from the space reserved for generation.
type Budget struct {
	ContextWindow int64 `json:"context_window"`
	ReserveTokens int64 `json:"reserve_tokens"`
	HighWater     int64 `json:"high_water"`
	Estimated     bool  `json:"estimated"`
}

// DeriveBudget reserves one quarter of a known window, capped at 16K tokens.
// Unknown models use a conservative 32K history budget plus a 16K reserve.
func DeriveBudget(contextWindow int64) Budget {
	if contextWindow <= 0 {
		return Budget{
			ContextWindow: UnknownHistoryBudgetTokens + UnknownReserveTokens,
			ReserveTokens: UnknownReserveTokens,
			HighWater:     UnknownHistoryBudgetTokens,
			Estimated:     true,
		}
	}
	reserve := contextWindow / 4
	if reserve < 1 {
		reserve = 1
	}
	if reserve > MaxReserveTokens {
		reserve = MaxReserveTokens
	}
	return Budget{
		ContextWindow: contextWindow,
		ReserveTokens: reserve,
		HighWater:     max(1, contextWindow-reserve),
	}
}

// DeriveBudgetForOutput uses observed generation size when the model window is
// known. Unknown models retain the conservative fallback.
func DeriveBudgetForOutput(contextWindow, priorOutputTokens int64) Budget {
	budget := DeriveBudget(contextWindow)
	if contextWindow <= 0 || priorOutputTokens <= 0 {
		return budget
	}
	reserve := priorOutputTokens * 2
	if reserve < MinAdaptiveReserveTokens {
		reserve = MinAdaptiveReserveTokens
	}
	if reserve > MaxAdaptiveReserveTokens {
		reserve = MaxAdaptiveReserveTokens
	}
	budget.ReserveTokens = reserve
	budget.HighWater = max(1, contextWindow-reserve)
	return budget
}

type CompactionPhase string

const (
	PhaseAuto       CompactionPhase = "auto"
	PhaseStandalone CompactionPhase = "standalone"
	PhasePreTurn    CompactionPhase = "pre_turn"
	PhaseMidTurn    CompactionPhase = "mid_turn"
)

type CheckpointLevel string

const (
	CheckpointLevelSegmented CheckpointLevel = "segmented"
	CheckpointLevelSession   CheckpointLevel = "session"
)

type Segment struct {
	FromSeq      Seq    `json:"from_seq"`
	ThroughSeq   Seq    `json:"through_seq"`
	SourceDigest string `json:"source_digest"`
	Summary      string `json:"summary"`
}

// Checkpoint is a lossy model-history projection over an immutable event prefix.
type Checkpoint struct {
	SchemaVersion        int             `json:"schema_version"`
	SourcePolicyVersion  string          `json:"source_policy_version"`
	SummaryFormatVersion string          `json:"summary_format_version"`
	PromptVersion        string          `json:"prompt_version"`
	CheckpointID         string          `json:"checkpoint_id"`
	PreviousCheckpointID string          `json:"previous_checkpoint_id,omitempty"`
	SessionID            string          `json:"session_id"`
	Phase                CompactionPhase `json:"phase"`
	HeadAnchorSeq        Seq             `json:"head_anchor_seq,omitempty"`
	ThroughSeq           Seq             `json:"through_seq"`
	SourceDigest         string          `json:"source_digest"`
	ProjectionKind       ProjectionKind  `json:"projection_kind"`
	Level                CheckpointLevel `json:"level,omitempty"`
	Segments             []Segment       `json:"segments,omitempty"`
	Summary              string          `json:"summary"`
	ProviderRoute        string          `json:"provider_route,omitempty"`
	ProviderStateKind    string          `json:"provider_state_kind,omitempty"`
	ProviderState        json.RawMessage `json:"provider_state,omitempty"`
	Model                string          `json:"model"`

	EstimatedRawTokens    int64 `json:"estimated_raw_tokens,omitempty"`
	EstimatedTokensBefore int64 `json:"estimated_tokens_before"`
	EstimatedTokensAfter  int64 `json:"estimated_tokens_after"`

	CreatedAt time.Time `json:"created_at"`
}

// AcceptedBoundary records the canonical history included in a successful
// physical provider request for one exact provider route.
type AcceptedBoundary struct {
	SessionID    string    `json:"session_id"`
	Route        string    `json:"route"`
	ThroughSeq   Seq       `json:"through_seq"`
	InputTokens  int64     `json:"input_tokens,omitempty"`
	OutputTokens int64     `json:"output_tokens,omitempty"`
	PayloadUnits int64     `json:"payload_units,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// Plan contains the immutable prefix to summarize and its rolling input.
type Plan struct {
	SessionID            string
	Phase                CompactionPhase
	HeadAnchorSeq        Seq
	HeadAnchor           *Message
	ThroughSeq           Seq
	SourceDigest         string
	SourceMessages       []Message
	PreviousSummary      string
	PreviousCheckpointID string
	CoveredMessages      int
	CoveredFromSeq       Seq
	NewFromSeq           Seq
	NewSourceDigest      string
	EstimatedRawTokens   int64
	EstimatedTokens      int64
}

type PlanOptions struct {
	Phase         CompactionPhase
	MaxThroughSeq Seq
}

// BuildPlanForPhase selects the largest safe contiguous prefix for a compaction phase.
func BuildPlanForPhase(events []Event, previous *Checkpoint, phase CompactionPhase) (Plan, bool) {
	return BuildPlanWithOptions(events, previous, PlanOptions{Phase: phase})
}

// BuildPlanWithOptions supports bounded retreat after a provider accepted a
// smaller canonical prefix.
func BuildPlanWithOptions(
	events []Event,
	previous *Checkpoint,
	options PlanOptions,
) (Plan, bool) {
	phase := options.Phase
	if phase == PhaseAuto {
		preTurn, preTurnOK := BuildPlanWithOptions(events, previous, PlanOptions{
			Phase: PhasePreTurn, MaxThroughSeq: options.MaxThroughSeq,
		})
		midTurn, midTurnOK := BuildPlanWithOptions(events, previous, PlanOptions{
			Phase: PhaseMidTurn, MaxThroughSeq: options.MaxThroughSeq,
		})
		switch {
		case preTurnOK && midTurnOK:
			if midTurn.ThroughSeq > preTurn.ThroughSeq {
				return midTurn, true
			}
			return preTurn, true
		case midTurnOK:
			return midTurn, true
		case preTurnOK:
			return preTurn, true
		default:
			return Plan{}, false
		}
	}

	content := messageEvents(events)
	if len(content) == 0 {
		return Plan{}, false
	}
	groups := atomicGroups(content)
	cut, headAnchorSeq, ok := planBoundary(content, groups, phase)
	if !ok {
		return Plan{}, false
	}
	if options.MaxThroughSeq != 0 &&
		content[cut-1].seq > options.MaxThroughSeq {
		desired := 0
		for desired < cut && content[desired].seq <= options.MaxThroughSeq {
			desired++
		}
		cut = safePrefixCut(groups, desired)
		if cut == 0 {
			return Plan{}, false
		}
		if phase == PhaseMidTurn {
			lastUser := lastUserIndex(content)
			if lastUser < 0 || cut <= lastUser+1 {
				return Plan{}, false
			}
		}
	}
	if cut == 0 {
		return Plan{}, false
	}

	covered := content[:cut]
	through := covered[len(covered)-1].seq
	previousValid := previous != nil && validCheckpoint(*previous, content)
	if previousValid && through <= previous.ThroughSeq {
		return Plan{}, false
	}

	start := 0
	previousSummary := ""
	previousCheckpointID := ""
	if previousValid {
		for i, item := range covered {
			if item.seq <= previous.ThroughSeq {
				start = i + 1
			}
		}
		previousSummary = previous.Summary
		previousCheckpointID = previous.CheckpointID
	}
	if start >= len(covered) {
		return Plan{}, false
	}

	source := make([]Message, 0, len(covered)-start+1)
	if previousValid &&
		previous.HeadAnchorSeq != 0 &&
		previous.HeadAnchorSeq != headAnchorSeq {
		if anchor, found := messageAtSeq(content, previous.HeadAnchorSeq); found {
			source = append(source, anchor.message)
		}
	}
	for _, item := range covered[start:] {
		source = append(source, item.message)
	}
	source, _ = BoundToolResults(source, MaxToolResultTokens)

	rawMessages := messagesOf(covered)
	effectiveBefore := rawMessages
	if previousValid {
		effectiveBefore = projectedCoveredPrefix(content, *previous, through)
	}
	var headAnchor *Message
	if headAnchorSeq != 0 {
		if anchor, found := messageAtSeq(content, headAnchorSeq); found {
			copied := anchor.message
			headAnchor = &copied
		}
	}
	return Plan{
		SessionID:            content[0].session,
		Phase:                phase,
		HeadAnchorSeq:        headAnchorSeq,
		HeadAnchor:           headAnchor,
		ThroughSeq:           through,
		SourceDigest:         digest(covered),
		SourceMessages:       source,
		PreviousSummary:      previousSummary,
		PreviousCheckpointID: previousCheckpointID,
		CoveredMessages:      cut,
		CoveredFromSeq:       covered[0].seq,
		NewFromSeq:           covered[start].seq,
		NewSourceDigest:      digest(covered[start:]),
		EstimatedRawTokens:   EstimateMessagesTokens(rawMessages),
		EstimatedTokens:      EstimateMessagesTokens(effectiveBefore),
	}, true
}

type ModelProjection struct {
	Messages     []Message
	ContextState *model.ContextState
	ThroughSeq   Seq
}

// Project returns the portable text projection. Provider-native checkpoints
// fall back to canonical history when no matching route is supplied.
func Project(events []Event, checkpoint *Checkpoint) []Message {
	return ProjectForRoute(events, checkpoint, "").Messages
}

// ProjectForRoute applies a text checkpoint or a route-bound provider-native state.
func ProjectForRoute(
	events []Event,
	checkpoint *Checkpoint,
	route string,
) ModelProjection {
	content := messageEvents(events)
	var through Seq
	if len(content) > 0 {
		through = content[len(content)-1].seq
	}
	if checkpoint == nil || !validCheckpoint(*checkpoint, content) {
		return ModelProjection{Messages: messagesOf(content), ThroughSeq: through}
	}
	if checkpoint.ProjectionKind == ProjectionProviderNative {
		if route == "" || route != checkpoint.ProviderRoute {
			return ModelProjection{Messages: messagesOf(content), ThroughSeq: through}
		}
		out := projectedTail(content, *checkpoint)
		return ModelProjection{
			Messages: out,
			ContextState: &model.ContextState{
				Kind: checkpoint.ProviderStateKind,
				Data: append(json.RawMessage(nil), checkpoint.ProviderState...),
			},
			ThroughSeq: through,
		}
	}
	out := []Message{CheckpointMessage(checkpoint.Summary)}
	out[0].EventSeq = uint64(checkpoint.ThroughSeq)
	out = append(out, projectedTail(content, *checkpoint)...)
	return ModelProjection{Messages: out, ThroughSeq: through}
}

func projectedTail(content []messageEvent, checkpoint Checkpoint) []Message {
	var out []Message
	if checkpoint.HeadAnchorSeq != 0 {
		if anchor, ok := messageAtSeq(content, checkpoint.HeadAnchorSeq); ok {
			out = append(out, anchor.message)
		}
	}
	for _, item := range content {
		if item.seq > checkpoint.ThroughSeq {
			out = append(out, item.message)
		}
	}
	return out
}

// ValidateCheckpoint verifies that a checkpoint still covers the exact source prefix.
func ValidateCheckpoint(events []Event, checkpoint Checkpoint) bool {
	return validCheckpoint(checkpoint, messageEvents(events))
}

// CheckpointMessage creates the provider-visible envelope used for a text checkpoint.
func CheckpointMessage(summary string) Message {
	return Message{
		Role:    RoleSystem,
		Content: "<context_checkpoint>\n" + summary + "\n</context_checkpoint>",
	}
}

// EstimateCheckpointTokens measures a candidate checkpoint as it will appear to the model.
func EstimateCheckpointTokens(summary string, anchor *Message) int64 {
	projected := []Message{CheckpointMessage(summary)}
	if anchor != nil {
		projected = append(projected, *anchor)
	}
	return EstimateMessagesTokens(projected)
}

// ComputeCheckpointID derives stable identity from the projection and its source lineage.
func ComputeCheckpointID(checkpoint Checkpoint) string {
	h := sha256.New()
	segments, _ := json.Marshal(checkpoint.Segments)
	_, _ = fmt.Fprintf(
		h,
		"%d\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%d\x00%d\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00",
		checkpoint.SchemaVersion,
		checkpoint.SourcePolicyVersion,
		checkpoint.SummaryFormatVersion,
		checkpoint.PromptVersion,
		checkpoint.SessionID,
		checkpoint.Phase,
		checkpoint.PreviousCheckpointID,
		checkpoint.ThroughSeq,
		checkpoint.HeadAnchorSeq,
		checkpoint.SourceDigest,
		checkpoint.ProjectionKind,
		checkpoint.Level,
		string(segments),
		checkpoint.ProviderRoute,
		checkpoint.ProviderStateKind,
		string(checkpoint.ProviderState),
		checkpoint.Summary,
		checkpoint.Model,
	)
	return hex.EncodeToString(h.Sum(nil))
}

// EstimateRequestTokens estimates the fully materialized request, including tool schemas.
func EstimateRequestTokens(messages []Message, tools []model.ToolDef) int64 {
	return unitsToTokens(RequestUnits(messages, tools))
}

// EstimateNextRequestTokens anchors on provider-reported input tokens and applies
// a signed payload-size delta. A cold start estimates the complete payload.
func EstimateNextRequestTokens(priorInputTokens, priorUnits, currentUnits int64) int64 {
	if priorInputTokens > 0 && priorUnits > 0 {
		return max(0, priorInputTokens+unitsToTokens(currentUnits-priorUnits))
	}
	return max(0, unitsToTokens(currentUnits))
}

// RequestUnits returns a stable weighted character count for delta estimation.
func RequestUnits(messages []Message, tools []model.ToolDef) int64 {
	return RequestUnitsWithContext(messages, tools, nil)
}

// RequestUnitsWithContext also accounts for opaque provider-native continuation state.
func RequestUnitsWithContext(
	messages []Message,
	tools []model.ToolDef,
	contextState *model.ContextState,
) int64 {
	payload, _ := json.Marshal(struct {
		Messages     []providerVisibleMessage `json:"messages"`
		Tools        []model.ToolDef          `json:"tools"`
		ContextState *model.ContextState      `json:"context_state,omitempty"`
	}{providerVisibleMessages(messages), tools, contextState})
	units := weightedUnits(string(payload))
	for _, item := range messages {
		units += int64(len(item.Attachments)) * 4096
	}
	return units
}

// EstimateMessagesTokens estimates a list of model-visible messages.
func EstimateMessagesTokens(messages []Message) int64 {
	payload, _ := json.Marshal(providerVisibleMessages(messages))
	return EstimateTextTokens(string(payload))
}

// EstimateTextTokens uses 4 ASCII characters per token and one token per
// non-ASCII rune. It intentionally favors a conservative estimate.
func EstimateTextTokens(text string) int64 { return unitsToTokens(weightedUnits(text)) }

// BoundToolResults replaces oversized model-visible tool payloads with a bounded excerpt.
// The canonical event log remains untouched.
func BoundToolResults(messages []Message, maxTokens int64) ([]Message, int) {
	return BoundToolResultsWithPolicy(messages, ToolResultPolicy{
		MaxTokens: maxTokens,
	})
}

type ToolResultPolicy struct {
	MaxTokens  int64
	KeepNewest int
}

// BoundToolResultsWithPolicy preserves a recent working set before projecting
// older oversized tool results to recoverable excerpts.
func BoundToolResultsWithPolicy(
	messages []Message,
	policy ToolResultPolicy,
) ([]Message, int) {
	out := append([]Message(nil), messages...)
	keep := make(map[int]struct{}, max(0, policy.KeepNewest))
	for i := len(out) - 1; i >= 0 && len(keep) < policy.KeepNewest; i-- {
		if out[i].Role == RoleTool {
			keep[i] = struct{}{}
		}
	}
	rewritten := 0
	for i := range out {
		if _, preserved := keep[i]; preserved {
			continue
		}
		if out[i].Role != RoleTool ||
			EstimateTextTokens(out[i].Content) <= policy.MaxTokens {
			continue
		}
		originalTokens := EstimateTextTokens(out[i].Content)
		notice := toolResultNotice(out[i], originalTokens)
		excerptTokens := max(
			1,
			policy.MaxTokens-EstimateTextTokens(notice)-EstimateTextTokens(toolOutputOmission),
		)
		out[i].Content = boundedHeadTail(out[i].Content, excerptTokens) + notice
		rewritten++
	}
	return out, rewritten
}

type messageEvent struct {
	seq     Seq
	session string
	message Message
}

type atomicGroup struct {
	start    int
	end      int
	complete bool
}

func atomicGroups(content []messageEvent) []atomicGroup {
	groups := make([]atomicGroup, 0, len(content))
	for i := 0; i < len(content); {
		item := content[i].message
		if item.Role == RoleAssistant && len(item.ToolCalls) > 0 {
			expected := make(map[string]struct{}, len(item.ToolCalls))
			for _, call := range item.ToolCalls {
				expected[call.ID] = struct{}{}
			}
			seen := make(map[string]struct{}, len(expected))
			j := i + 1
			for j < len(content) && content[j].message.Role == RoleTool {
				if _, ok := expected[content[j].message.ToolCallID]; ok {
					seen[content[j].message.ToolCallID] = struct{}{}
				}
				j++
			}
			groups = append(groups, atomicGroup{
				start:    i,
				end:      j,
				complete: len(expected) > 0 && len(seen) == len(expected),
			})
			i = j
			continue
		}
		groups = append(groups, atomicGroup{
			start:    i,
			end:      i + 1,
			complete: item.Role != RoleTool,
		})
		i++
	}
	return groups
}

func planBoundary(
	content []messageEvent,
	groups []atomicGroup,
	phase CompactionPhase,
) (int, Seq, bool) {
	switch phase {
	case PhaseStandalone:
		cut := safePrefixCut(groups, len(content))
		return cut, 0, cut > 0
	case PhasePreTurn:
		lastUser := lastUserIndex(content)
		if lastUser < 0 {
			return 0, 0, false
		}
		cut := safePrefixCut(groups, lastUser)
		return cut, 0, cut > 0
	case PhaseMidTurn:
		lastUser := lastUserIndex(content)
		if lastUser < 0 {
			return 0, 0, false
		}
		headGroup := -1
		for i, group := range groups {
			if group.start <= lastUser && lastUser < group.end {
				headGroup = i
				break
			}
		}
		// Keep the latest atomic group verbatim and require at least one completed
		// group after the user head to make the fold worthwhile.
		if headGroup < 0 || len(groups)-headGroup-1 < 2 {
			return 0, 0, false
		}
		lastCoveredGroup := len(groups) - 2
		for i := 0; i <= lastCoveredGroup; i++ {
			if !groups[i].complete {
				return 0, 0, false
			}
		}
		cut := groups[lastCoveredGroup].end
		return cut, content[lastUser].seq, cut > lastUser+1
	default:
		return 0, 0, false
	}
}

func safePrefixCut(groups []atomicGroup, desired int) int {
	cut := 0
	for _, group := range groups {
		if group.end > desired || !group.complete {
			break
		}
		cut = group.end
	}
	return cut
}

func lastUserIndex(content []messageEvent) int {
	for i := len(content) - 1; i >= 0; i-- {
		if content[i].message.Role == RoleUser {
			return i
		}
	}
	return -1
}

func messageAtSeq(content []messageEvent, seq Seq) (messageEvent, bool) {
	for _, item := range content {
		if item.seq == seq {
			return item, true
		}
	}
	return messageEvent{}, false
}

func projectedCoveredPrefix(
	content []messageEvent,
	checkpoint Checkpoint,
	through Seq,
) []Message {
	out := []Message{CheckpointMessage(checkpoint.Summary)}
	if checkpoint.HeadAnchorSeq != 0 {
		if anchor, ok := messageAtSeq(content, checkpoint.HeadAnchorSeq); ok {
			out = append(out, anchor.message)
		}
	}
	for _, item := range content {
		if item.seq > checkpoint.ThroughSeq && item.seq <= through {
			out = append(out, item.message)
		}
	}
	return out
}

type providerVisibleMessage struct {
	Role       Role       `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

func providerVisibleMessages(messages []Message) []providerVisibleMessage {
	out := make([]providerVisibleMessage, 0, len(messages))
	for _, item := range messages {
		if item.EmptyAssistantForModel() {
			continue
		}
		out = append(out, providerVisibleMessage{
			Role:       item.Role,
			Content:    item.ModelContent(),
			ToolCalls:  item.ToolCalls,
			ToolCallID: item.ToolCallID,
		})
	}
	return out
}

func messageEvents(events []Event) []messageEvent {
	out := make([]messageEvent, 0, len(events))
	for _, ev := range events {
		if ev.Kind != KindMessageEnd && ev.Kind != KindMessageImported {
			continue
		}
		msg, ok := ev.Payload.(Message)
		if ok {
			msg.EventSeq = uint64(ev.Seq)
			out = append(out, messageEvent{
				seq:     ev.Seq,
				session: ev.Session,
				message: msg,
			})
		}
	}
	return out
}

func messagesOf(events []messageEvent) []Message {
	out := make([]Message, 0, len(events))
	for _, item := range events {
		out = append(out, item.message)
	}
	return out
}

func validCheckpoint(checkpoint Checkpoint, content []messageEvent) bool {
	if checkpoint.ThroughSeq == 0 ||
		checkpoint.SessionID == "" ||
		checkpoint.Model == "" ||
		checkpoint.CreatedAt.IsZero() {
		return false
	}
	if len(content) > 0 &&
		checkpoint.SessionID != content[0].session {
		return false
	}
	if checkpoint.SchemaVersion != CheckpointSchemaVersion ||
		checkpoint.SourcePolicyVersion != SourcePolicyVersion ||
		checkpoint.SummaryFormatVersion != SummaryFormatVersion ||
		checkpoint.PromptVersion != PromptVersion ||
		checkpoint.SourceDigest == "" {
		return false
	}
	if checkpoint.Phase != PhaseStandalone &&
		checkpoint.Phase != PhasePreTurn &&
		checkpoint.Phase != PhaseMidTurn {
		return false
	}
	if checkpoint.ProjectionKind != ProjectionText &&
		checkpoint.ProjectionKind != ProjectionProviderNative {
		return false
	}
	if checkpoint.ProjectionKind == ProjectionText {
		if strings.TrimSpace(checkpoint.Summary) == "" ||
			len(checkpoint.Segments) == 0 ||
			(checkpoint.Level != CheckpointLevelSegmented &&
				checkpoint.Level != CheckpointLevelSession) {
			return false
		}
	}
	if checkpoint.ProjectionKind == ProjectionProviderNative {
		if checkpoint.ProviderRoute == "" ||
			checkpoint.ProviderStateKind == "" ||
			len(checkpoint.ProviderState) == 0 ||
			!json.Valid(checkpoint.ProviderState) {
			return false
		}
	}
	if !validSegments(checkpoint) {
		return false
	}
	if checkpoint.CheckpointID == "" ||
		checkpoint.CheckpointID != ComputeCheckpointID(checkpoint) {
		return false
	}
	cut := 0
	for _, item := range content {
		if item.seq <= checkpoint.ThroughSeq {
			cut++
		}
	}
	if cut == 0 ||
		content[cut-1].seq != checkpoint.ThroughSeq ||
		digest(content[:cut]) != checkpoint.SourceDigest {
		return false
	}
	if checkpoint.HeadAnchorSeq != 0 {
		anchor, ok := messageAtSeq(content[:cut], checkpoint.HeadAnchorSeq)
		if !ok || anchor.message.Role != RoleUser {
			return false
		}
	}
	return true
}

func digest(events []messageEvent) string {
	h := sha256.New()
	for _, item := range events {
		_, _ = fmt.Fprintf(h, "%d\x00", item.seq)
		msg := item.message
		msg.EventSeq = 0
		data, _ := json.Marshal(msg)
		_, _ = h.Write(data)
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func weightedUnits(text string) int64 {
	var units int64
	for _, r := range text {
		if r <= 0x7f {
			units++
		} else {
			units += 4
		}
	}
	return units
}

func unitsToTokens(units int64) int64 {
	if units == 0 {
		return 0
	}
	if units < 0 {
		return -((-units + 3) / 4)
	}
	return (units + 3) / 4
}

func boundedHeadTail(text string, maxTokens int64) string {
	if maxTokens <= 0 {
		return ""
	}
	maxUnits := maxTokens * 4
	runes := []rune(text)
	if weightedUnits(text) <= maxUnits {
		return strings.TrimSpace(text)
	}
	headEnd := prefixRunesWithin(runes, maxUnits*3/5)
	tailStart := suffixRunesWithin(runes, maxUnits-maxUnits*3/5)
	if tailStart < headEnd {
		tailStart = headEnd
	}
	return strings.TrimSpace(string(runes[:headEnd])) +
		toolOutputOmission +
		strings.TrimSpace(string(runes[tailStart:]))
}

func prefixRunesWithin(runes []rune, maxUnits int64) int {
	var used int64
	for i, r := range runes {
		cost := runeUnits(r)
		if used+cost > maxUnits {
			return i
		}
		used += cost
	}
	return len(runes)
}

func suffixRunesWithin(runes []rune, maxUnits int64) int {
	var used int64
	for i := len(runes) - 1; i >= 0; i-- {
		cost := runeUnits(runes[i])
		if used+cost > maxUnits {
			return i + 1
		}
		used += cost
	}
	return 0
}

func runeUnits(r rune) int64 {
	if r > 0x7f {
		return 4
	}
	return 1
}

func toolResultNotice(item Message, originalTokens int64) string {
	if item.EventSeq == 0 || strings.TrimSpace(item.ToolCallID) == "" {
		return fmt.Sprintf(
			"\n\n[Tool output reduced from approximately %d tokens. The beginning and end are preserved. Re-run the tool with narrower parameters if exact details are needed.]",
			originalTokens,
		)
	}
	sum := sha256.Sum256([]byte(item.Content))
	reference, _ := json.Marshal(struct {
		Kind            string `json:"kind"`
		EventSeq        uint64 `json:"event_seq"`
		ToolCallID      string `json:"tool_call_id"`
		SHA256          string `json:"sha256"`
		EstimatedTokens int64  `json:"estimated_tokens"`
		ReadTool        string `json:"read_tool"`
	}{
		Kind:            "foya.tool_result_ref.v1",
		EventSeq:        item.EventSeq,
		ToolCallID:      item.ToolCallID,
		SHA256:          hex.EncodeToString(sum[:]),
		EstimatedTokens: originalTokens,
		ReadTool:        "history_read_tool_result",
	})
	return "\n\n<tool_result_ref>" + string(reference) + "</tool_result_ref>"
}
