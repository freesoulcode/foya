// Package event defines kernel event types and monotonic sequence numbers.
//
// Events are the source of truth. Chat state, UI views, and crash recovery are
// projections of this stream. Monotonic sequence numbers support SSE replay
// through Last-Event-ID and continuation across devices.
package conversation

import "time"

// Seq is a monotonically increasing event number within a chat.
type Seq uint64

// Kind identifies an event type.
type Kind string

const (
	KindMessageDelta             Kind = "message_delta"     // Streamed text delta.
	KindReasoningDelta           Kind = "reasoning_delta"   // Streamed reasoning delta.
	KindMessageEnd               Kind = "message_end"       // Completed message.
	KindToolBegin                Kind = "tool_begin"        // Tool call entered the queue.
	KindToolUpdate               Kind = "tool_update"       // Tool arguments, state, or partial result.
	KindToolEnd                  Kind = "tool_end"          // Completed tool call.
	KindApprovalReq              Kind = "approval_request"  // Must-deliver approval request.
	KindApprovalResolved         Kind = "approval_resolved" // Must-deliver approval decision.
	KindQuestionRequested        Kind = "question_requested"
	KindQuestionResolved         Kind = "question_resolved"
	KindBrowserActionRequested   Kind = "browser_action_requested"
	KindBrowserActionResolved    Kind = "browser_action_resolved"
	KindSessionUpdated           Kind = "session_updated" // Must-deliver chat metadata update.
	KindSessionDeleted           Kind = "session_deleted" // Must-deliver chat deletion.
	KindQueueUpdated             Kind = "queue_updated"   // Must-deliver queue snapshot.
	KindUsageUpdated             Kind = "usage_updated"   // Latest model request token usage.
	KindCompactionStarted        Kind = "compaction_started"
	KindCompactionCompleted      Kind = "compaction_completed"
	KindCompactionFailed         Kind = "compaction_failed"
	KindCompactionDiagnostic     Kind = "compaction_diagnostic"
	KindContextRequestAccepted   Kind = "context_request_accepted"
	KindHistoryRewound           Kind = "history_rewound"
	KindFileReviewResolved       Kind = "file_review_resolved"
	KindMessageImported          Kind = "message_imported"
	KindSessionForked            Kind = "session_forked"
	KindTurnStarted              Kind = "turn_started"
	KindTurnCancelRequested      Kind = "turn_cancel_requested"
	KindTurnComplete             Kind = "turn_complete" // Must-deliver turn completion.
	KindSubAgentQueued           Kind = "subagent_queued"
	KindSubAgentRunning          Kind = "subagent_running"
	KindSubAgentStarted          Kind = "subagent_started"
	KindSubAgentCompleted        Kind = "subagent_completed"
	KindSubAgentFailed           Kind = "subagent_failed"
	KindSubAgentCancelled        Kind = "subagent_cancelled"
	KindSubAgentInterrupted      Kind = "subagent_interrupted"
	KindAgentBudgetUpdated       Kind = "agent_budget_updated"
	KindAgentBudgetExceeded      Kind = "agent_budget_exceeded"
	KindHookCompleted            Kind = "hook_completed"
	KindWorkflowUpdated          Kind = "workflow_updated"
	KindCanvasCreated            Kind = "canvas_created"
	KindCanvasUpdated            Kind = "canvas_updated"
	KindCanvasDeleted            Kind = "canvas_deleted"
	KindBackgroundCommandUpdated Kind = "background_command_updated"
	KindError                    Kind = "error"
)

// RewindFileResult records how one tracked file was handled by a history rewind.
type RewindFileResult struct {
	Path   string `json:"path"`
	Action string `json:"action"` // restored / merged / kept / force_restored
}

const (
	RewindFileRestored      = "restored"
	RewindFileMerged        = "merged"
	RewindFileKept          = "kept"
	RewindFileForceRestored = "force_restored"
)

// HistoryRewound marks the active history as the prefix before TargetUserSeq
// and records the files restored while applying the rewind.
type HistoryRewound struct {
	TargetUserSeq Seq                `json:"target_user_seq"`
	Files         []RewindFileResult `json:"files,omitempty"`
}

// FileReviewResolved records one explicit keep-all or undo-all decision.
type FileReviewResolved struct {
	Action     string             `json:"action"` // kept / undone
	ThroughSeq Seq                `json:"through_seq"`
	Files      []RewindFileResult `json:"files,omitempty"`
}

const (
	FileReviewKept   = "kept"
	FileReviewUndone = "undone"
)

// SessionForked records that a new session imported the active history of an
// existing session. The imported messages are copied as independent events.
type SessionForked struct {
	SourceSessionID string    `json:"source_session_id"`
	ThroughSeq      Seq       `json:"through_seq"`
	ForkedAt        time.Time `json:"forked_at"`
	MessageCount    int       `json:"message_count"`
}

// Event is a chat event envelope whose payload type depends on Kind.
type Event struct {
	Seq     Seq       `json:"seq"`
	Kind    Kind      `json:"kind"`
	Session string    `json:"session"`
	RunID   string    `json:"run_id,omitempty"`
	Time    time.Time `json:"time"`
	Payload any       `json:"payload,omitempty"`
}
