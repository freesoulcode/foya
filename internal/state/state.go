// Package state implements the canonical event log and its projections.
package state

import (
	"context"
	"errors"
	"time"

	"github.com/freesoulcode/foya/internal/compaction"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/message"
)

var (
	ErrActiveUserMessageNotFound = errors.New("active user message not found")
	ErrHistoryChanged            = errors.New("active history changed after rewind preview")
	ErrFileBlobNotFound          = errors.New("file blob not found")
	ErrFileReviewChanged         = errors.New("pending file changes changed after preview")
	ErrNoPendingFileChanges      = errors.New("no pending file changes")
)

// RewindResult reports a rewind preview or committed history rewind.
type RewindResult struct {
	Event       event.Event
	Message     message.Message
	FileChanges []RewindFileChange
	HeadSeq     event.Seq
	FirstUser   bool
	Applied     bool
}

// RewindFileChange is one completed write/edit operation to reverse.
type RewindFileChange struct {
	EventSeq event.Seq
	Change   message.FileChange
}

type FileReview struct {
	Changes    []RewindFileChange
	ThroughSeq event.Seq
}

type FileRewindBackup struct {
	Path          string
	BeforeExists  bool
	BeforeMode    uint32
	BeforeContent []byte
	BeforeBlob    string
	AfterExists   bool
	AfterMode     uint32
	AfterContent  []byte
	AfterBlob     string
}

type FileRewindJournal struct {
	ID              string
	SessionID       string
	TargetUserSeq   event.Seq
	ExpectedHeadSeq event.Seq
	State           string
	Files           []FileRewindBackup
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
	FileBlob(ctx context.Context, hash string) ([]byte, error)
	BeginFileRewind(
		ctx context.Context,
		session string,
		targetUserSeq event.Seq,
		expectedHeadSeq event.Seq,
		files []FileRewindBackup,
	) (string, error)
	PendingFileRewinds(ctx context.Context) ([]FileRewindJournal, error)
	FinishFileRewind(ctx context.Context, id string) error
	PruneFileCheckpoints(ctx context.Context, now time.Time) error
	PendingFileReview(ctx context.Context, session string) (FileReview, error)
	ResolveFileReview(
		ctx context.Context,
		session string,
		expectedThroughSeq event.Seq,
		action string,
		fileResults []event.RewindFileResult,
		journalID string,
	) (event.Event, error)
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
	Rewind(
		ctx context.Context,
		session string,
		targetUserSeq event.Seq,
		confirm bool,
		expectedHeadSeq event.Seq,
		fileResults []event.RewindFileResult,
		journalID string,
	) (RewindResult, error)
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
