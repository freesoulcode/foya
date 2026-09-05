// Package event 定义内核事件类型与单调序号。
//
// 事件是「日志即真相」的原子单位:内核产生的一切事实都是 Event,
// 会话状态、UI 视图、崩溃恢复都是事件流的投影。每个事件带单调递增
// 的 Seq,服务于 SSE 断线补发(Last-Event-ID)与多设备续接。
package event

import "time"

// Seq 是会话内单调递增的事件序号。
type Seq uint64

// Kind 标识事件类型。
type Kind string

const (
	KindMessageDelta             Kind = "message_delta"     // 流式 token 增量
	KindReasoningDelta           Kind = "reasoning_delta"   // 流式思考内容增量(思考模型)
	KindMessageEnd               Kind = "message_end"       // 一条消息完成
	KindToolBegin                Kind = "tool_begin"        // 模型已生成工具调用,进入等待队列
	KindToolUpdate               Kind = "tool_update"       // 参数、执行状态或部分结果更新
	KindToolEnd                  Kind = "tool_end"          // 工具调用结束
	KindApprovalReq              Kind = "approval_request"  // 审批请求(必达)
	KindApprovalResolved         Kind = "approval_resolved" // 审批已决策(必达)
	KindQuestionRequested        Kind = "question_requested"
	KindQuestionResolved         Kind = "question_resolved"
	KindBrowserActionRequested   Kind = "browser_action_requested"
	KindBrowserActionResolved    Kind = "browser_action_resolved"
	KindSessionUpdated           Kind = "session_updated" // 会话元数据变更(标题/模型等,必达)
	KindSessionDeleted           Kind = "session_deleted" // 会话被删除(必达,前端据此移除)
	KindQueueUpdated             Kind = "queue_updated"   // 待发送队列完整快照(必达)
	KindUsageUpdated             Kind = "usage_updated"   // 最近一次模型请求 token 使用情况
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
	KindTurnComplete             Kind = "turn_complete" // 回合结束(必达)
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

// Event 是内核事件的信封。Payload 由 Kind 决定其具体类型。
type Event struct {
	Seq     Seq       `json:"seq"`
	Kind    Kind      `json:"kind"`
	Session string    `json:"session"`
	RunID   string    `json:"run_id,omitempty"`
	Time    time.Time `json:"time"`
	Payload any       `json:"payload,omitempty"`
}
