// Package agent implements the turn engine that builds context, calls a model,
// executes tool calls, feeds results back, and decides whether to continue.
//
// Turns run in cancellable background goroutines. Each chat is serial while
// different chats can run concurrently.
package agent

import "context"

// TurnKind identifies a turn type.
type TurnKind string

const (
	TurnRegular  TurnKind = "regular"  // Regular chat turn.
	TurnCompact  TurnKind = "compact"  // Context compaction.
	TurnSubAgent TurnKind = "subagent" // Sub-agent turn.
)

// StopReason explains why a turn ended.
type StopReason string

const (
	StopEnd       StopReason = "stop"       // The model finished normally.
	StopToolCalls StopReason = "tool_calls" // Tool execution must continue.
	StopLength    StopReason = "length"     // The token limit was reached.
	StopError     StopReason = "error"
	StopAborted   StopReason = "aborted" // The turn was cancelled.
)

// TurnInput contains the input for one turn.
type TurnInput struct {
	Message string // Placeholder for a future structured message.
}

// TurnResult is the outcome of one turn.
type TurnResult struct {
	NeedsFollowUp bool
	StopReason    StopReason
}

// Turn is a cancellable turn task.
type Turn interface {
	Run(ctx context.Context, in TurnInput) (TurnResult, error)
	Kind() TurnKind
}

// Loop runs turns serially per chat and concurrently across chats.
type Loop interface {
	// Submit starts a turn or applies the configured queue policy.
	Submit(sessionID string, in TurnInput) (runID string, err error)
	// Cancel stops the active turn for a chat.
	Cancel(sessionID string)
	// Steer injects input into a running turn.
	Steer(sessionID string, msg string)
}
