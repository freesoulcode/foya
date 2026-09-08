// Package state implements the canonical event log and its projections.
package conversation

import (
	"context"
	"errors"
	"time"
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
	Event       Event
	Message     Message
	FileChanges []RewindFileChange
	HeadSeq     Seq
	FirstUser   bool
	Applied     bool
}

// RewindFileChange is one completed write/edit operation to reverse.
type RewindFileChange struct {
	EventSeq Seq
	Change   FileChange
}

type FileReview struct {
	Changes    []RewindFileChange
	ThroughSeq Seq
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
	TargetUserSeq   Seq
	ExpectedHeadSeq Seq
	State           string
	Files           []FileRewindBackup
}

// Log is the minimal append and replay contract.
type Log interface {
	Append(ctx context.Context, ev Event) (Seq, error)
	Read(ctx context.Context, session string, after Seq) ([]Event, error)
}

// Store is the complete event storage contract used by the runtime.
type Store interface {
	Log
	Delete(ctx context.Context, session string) error
	History(ctx context.Context, session string) ([]Message, error)
	ModelHistory(ctx context.Context, session string) ([]Message, error)
	ModelContext(
		ctx context.Context,
		session string,
		route string,
	) (ModelProjection, error)
	Events(ctx context.Context, session string) ([]Event, error)
	ActiveMessageEvent(
		ctx context.Context,
		session string,
		seq Seq,
	) (Event, bool, error)
	FileBlob(ctx context.Context, hash string) ([]byte, error)
	BeginFileRewind(
		ctx context.Context,
		session string,
		targetUserSeq Seq,
		expectedHeadSeq Seq,
		files []FileRewindBackup,
	) (string, error)
	PendingFileRewinds(ctx context.Context) ([]FileRewindJournal, error)
	FinishFileRewind(ctx context.Context, id string) error
	PruneFileCheckpoints(ctx context.Context, now time.Time) error
	PendingFileReview(ctx context.Context, session string) (FileReview, error)
	ResolveFileReview(
		ctx context.Context,
		session string,
		expectedThroughSeq Seq,
		action string,
		fileResults []RewindFileResult,
		journalID string,
	) (Event, error)
	UsageSummary(ctx context.Context, query UsageQuery) (UsageSummary, error)
	Checkpoint(ctx context.Context, session string) (*Checkpoint, bool, error)
	AcceptedBoundary(
		ctx context.Context,
		session string,
		route string,
	) (AcceptedBoundary, bool, error)
	RecordCheckpoint(ctx context.Context, checkpoint Checkpoint) (Event, error)
	ImportMessages(
		ctx context.Context,
		session string,
		sourceSession string,
		throughSeq Seq,
		messages []Message,
	) ([]Event, error)
	Rewind(
		ctx context.Context,
		session string,
		targetUserSeq Seq,
		confirm bool,
		expectedHeadSeq Seq,
		fileResults []RewindFileResult,
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

// Projection derives a view from the event log.
type Projection[V any] interface {
	// Project builds a chat view through the requested sequence.
	Project(ctx context.Context, session string, upto Seq) (V, error)
}
