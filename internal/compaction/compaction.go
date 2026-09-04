// Package compaction provides context-budget estimation and immutable history projections.
package compaction

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/provider"
)

const (
	UnknownHistoryBudgetTokens int64 = 32_000
	UnknownReserveTokens       int64 = 16_384
	MaxReserveTokens           int64 = 16_384
	MaxToolResultTokens        int64 = 2_048
)

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

// Checkpoint is a lossy model-history projection over an immutable event prefix.
type Checkpoint struct {
	SessionID             string    `json:"session_id"`
	ThroughSeq            event.Seq `json:"through_seq"`
	SourceDigest          string    `json:"source_digest"`
	Summary               string    `json:"summary"`
	Model                 string    `json:"model"`
	EstimatedTokensBefore int64     `json:"estimated_tokens_before"`
	EstimatedTokensAfter  int64     `json:"estimated_tokens_after"`
	CreatedAt             time.Time `json:"created_at"`
}

// Plan contains the immutable prefix to summarize and its rolling input.
type Plan struct {
	SessionID       string
	ThroughSeq      event.Seq
	SourceDigest    string
	SourceMessages  []message.Message
	PreviousSummary string
	CoveredMessages int
	EstimatedTokens int64
}

// BuildPlan selects a completed message prefix. Automatic compaction preserves
// the latest user turn verbatim; standalone/manual compaction may cover all messages.
func BuildPlan(events []event.Event, previous *Checkpoint, preserveLatestTurn bool) (Plan, bool) {
	content := messageEvents(events)
	cut := len(content)
	if preserveLatestTurn {
		lastUser := -1
		for i := len(content) - 1; i >= 0; i-- {
			if content[i].message.Role == message.RoleUser {
				lastUser = i
				break
			}
		}
		if lastUser < 0 {
			return Plan{}, false
		}
		cut = lastUser
	}
	if cut == 0 {
		return Plan{}, false
	}

	covered := content[:cut]
	through := covered[len(covered)-1].seq
	start := 0
	previousSummary := ""
	if previous != nil && validCheckpoint(*previous, content) {
		if previous.ThroughSeq == through {
			return Plan{}, false
		}
		if previous.ThroughSeq < through {
			for i, item := range covered {
				if item.seq <= previous.ThroughSeq {
					start = i + 1
				}
			}
			previousSummary = previous.Summary
		}
	}
	if start >= len(covered) {
		return Plan{}, false
	}

	source := make([]message.Message, 0, len(covered)-start)
	for _, item := range covered[start:] {
		source = append(source, item.message)
	}
	source, _ = BoundToolResults(source, MaxToolResultTokens)
	return Plan{
		SessionID:       events[0].Session,
		ThroughSeq:      through,
		SourceDigest:    digest(covered),
		SourceMessages:  source,
		PreviousSummary: previousSummary,
		CoveredMessages: cut,
		EstimatedTokens: EstimateMessagesTokens(messagesOf(covered)),
	}, true
}

// Project replaces a validated covered prefix with its checkpoint summary.
func Project(events []event.Event, checkpoint *Checkpoint) []message.Message {
	content := messageEvents(events)
	if checkpoint == nil || !validCheckpoint(*checkpoint, content) {
		return messagesOf(content)
	}
	out := []message.Message{{
		Role:    message.RoleSystem,
		Content: "<context_checkpoint>\n" + checkpoint.Summary + "\n</context_checkpoint>",
	}}
	for _, item := range content {
		if item.seq > checkpoint.ThroughSeq {
			out = append(out, item.message)
		}
	}
	return out
}

// ValidateCheckpoint verifies that a checkpoint still covers the exact source prefix.
func ValidateCheckpoint(events []event.Event, checkpoint Checkpoint) bool {
	return validCheckpoint(checkpoint, messageEvents(events))
}

// EstimateRequestTokens estimates the fully materialized request, including tool schemas.
func EstimateRequestTokens(messages []message.Message, tools []provider.ToolDef) int64 {
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
func RequestUnits(messages []message.Message, tools []provider.ToolDef) int64 {
	payload, _ := json.Marshal(struct {
		Messages []providerVisibleMessage `json:"messages"`
		Tools    []provider.ToolDef       `json:"tools"`
	}{providerVisibleMessages(messages), tools})
	units := weightedUnits(string(payload))
	for _, item := range messages {
		units += int64(len(item.Attachments)) * 4096
	}
	return units
}

// EstimateMessagesTokens estimates a list of model-visible messages.
func EstimateMessagesTokens(messages []message.Message) int64 {
	payload, _ := json.Marshal(providerVisibleMessages(messages))
	return EstimateTextTokens(string(payload))
}

// EstimateTextTokens uses 4 ASCII characters per token and one token per
// non-ASCII rune. It intentionally favors a conservative estimate.
func EstimateTextTokens(text string) int64 { return unitsToTokens(weightedUnits(text)) }

// BoundToolResults replaces oversized model-visible tool payloads with a bounded excerpt.
// The canonical event log remains untouched.
func BoundToolResults(messages []message.Message, maxTokens int64) ([]message.Message, int) {
	out := append([]message.Message(nil), messages...)
	rewritten := 0
	for i := range out {
		if out[i].Role != message.RoleTool || EstimateTextTokens(out[i].Content) <= maxTokens {
			continue
		}
		originalTokens := EstimateTextTokens(out[i].Content)
		out[i].Content = boundedExcerpt(out[i].Content, maxTokens) + fmt.Sprintf(
			"\n\n[Tool output reduced from approximately %d tokens. Re-run the tool with narrower parameters if exact details are needed.]",
			originalTokens,
		)
		rewritten++
	}
	return out, rewritten
}

type messageEvent struct {
	seq     event.Seq
	message message.Message
}

type providerVisibleMessage struct {
	Role       message.Role       `json:"role"`
	Content    string             `json:"content,omitempty"`
	ToolCalls  []message.ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string             `json:"tool_call_id,omitempty"`
}

func providerVisibleMessages(messages []message.Message) []providerVisibleMessage {
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

func messageEvents(events []event.Event) []messageEvent {
	out := make([]messageEvent, 0, len(events))
	for _, ev := range events {
		if ev.Kind != event.KindMessageEnd {
			continue
		}
		msg, ok := ev.Payload.(message.Message)
		if ok {
			out = append(out, messageEvent{seq: ev.Seq, message: msg})
		}
	}
	return out
}

func messagesOf(events []messageEvent) []message.Message {
	out := make([]message.Message, 0, len(events))
	for _, item := range events {
		out = append(out, item.message)
	}
	return out
}

func validCheckpoint(checkpoint Checkpoint, content []messageEvent) bool {
	if checkpoint.ThroughSeq == 0 || strings.TrimSpace(checkpoint.Summary) == "" {
		return false
	}
	cut := 0
	for _, item := range content {
		if item.seq <= checkpoint.ThroughSeq {
			cut++
		}
	}
	return cut > 0 &&
		content[cut-1].seq == checkpoint.ThroughSeq &&
		digest(content[:cut]) == checkpoint.SourceDigest
}

func digest(events []messageEvent) string {
	h := sha256.New()
	for _, item := range events {
		_, _ = fmt.Fprintf(h, "%d\x00", item.seq)
		data, _ := json.Marshal(item.message)
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

func boundedExcerpt(text string, maxTokens int64) string {
	if maxTokens <= 0 {
		return ""
	}
	maxUnits := maxTokens * 4
	runes := []rune(text)
	var used int64
	end := 0
	for end < len(runes) {
		cost := int64(1)
		if runes[end] > 0x7f {
			cost = 4
		}
		if used+cost > maxUnits {
			break
		}
		used += cost
		end++
	}
	return strings.TrimSpace(string(runes[:end]))
}
