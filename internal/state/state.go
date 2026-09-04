// Package state implements the canonical event log and its projections.
package state

import (
	"context"
	"errors"

	"github.com/freesoulcode/foya/internal/compaction"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/message"
)

var (
	ErrActiveUserMessageNotFound = errors.New("active user message not found")
	ErrMessageUnchanged          = errors.New("edited message is unchanged")
	ErrBranchChanged             = errors.New("active history changed after branch preview")
)

// BranchResult reports a branch preview or committed history branch.
type BranchResult struct {
	Event     event.Event
	Effects   []event.BranchEffect
	HeadSeq   event.Seq
	FirstUser bool
	Applied   bool
}

// Log is the minimal append and replay contract.
type Log interface {
	Append(ctx context.Context, ev event.Event) (event.Seq, error)
	Read(ctx context.Context, session string, after event.Seq) ([]event.Event, error)
}

// Store is the complete event storage contract used by the runtime.
type Store interface {
	Log
	Delete(ctx context.Context, session string) error
	History(ctx context.Context, session string) ([]message.Message, error)
	ModelHistory(ctx context.Context, session string) ([]message.Message, error)
	Events(ctx context.Context, session string) ([]event.Event, error)
	UsageSummary(ctx context.Context, query UsageQuery) (UsageSummary, error)
	Checkpoint(ctx context.Context, session string) (*compaction.Checkpoint, bool, error)
	RecordCheckpoint(ctx context.Context, checkpoint compaction.Checkpoint) (event.Event, error)
	ImportMessages(
		ctx context.Context,
		session string,
		sourceSession string,
		throughSeq event.Seq,
		messages []message.Message,
	) ([]event.Event, error)
	Branch(
		ctx context.Context,
		session string,
		targetUserSeq event.Seq,
		editedContent string,
		allowEffects bool,
		expectedHeadSeq event.Seq,
	) (BranchResult, error)
}

// UsageQuery selects historical usage aggregates by local-date strings.
type UsageQuery struct {
	RangeStart    string
	RangeEnd      string
	ActivityStart string
	Today         string
}

// UsageDay is one day of historical activity.
type UsageDay struct {
	Date         string
	MessageCount int
	TokenCount   int64
}

// UsageModelTotal is one model's historical usage in a query range.
type UsageModelTotal struct {
	Model        string
	TokenCount   int64
	RequestCount int
}

// UsageSummary is the historical usage projection retained after session deletion.
type UsageSummary struct {
	TotalTokens   int64
	InputTokens   int64
	OutputTokens  int64
	CachedTokens  int64
	SessionCount  int
	MessageCount  int
	ActiveDays    int
	CurrentStreak int
	Activity      []UsageDay
	ModelUsage    []UsageModelTotal
}

// Projection 从事件日志派生某种视图。
type Projection[V any] interface {
	// Project 将某会话到指定序号为止的事件投影成视图。
	Project(ctx context.Context, session string, upto event.Seq) (V, error)
}
