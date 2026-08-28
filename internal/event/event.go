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
	KindMessageDelta   Kind = "message_delta"    // 流式 token 增量
	KindReasoningDelta Kind = "reasoning_delta"   // 流式思考内容增量(思考模型)
	KindMessageEnd     Kind = "message_end"      // 一条消息完成
	KindToolBegin      Kind = "tool_begin"       // 工具调用开始
	KindToolUpdate     Kind = "tool_update"      // 工具执行中的部分输出
	KindToolEnd        Kind = "tool_end"         // 工具调用结束
	KindApprovalReq    Kind = "approval_request" // 审批请求(必达)
	KindApprovalResolved Kind = "approval_resolved" // 审批已决策(必达)
	KindSessionUpdated Kind = "session_updated"   // 会话元数据变更(标题/模型等,必达)
	KindTurnStarted    Kind = "turn_started"
	KindTurnComplete   Kind = "turn_complete" // 回合结束(必达)
	KindError          Kind = "error"
)

// Event 是内核事件的信封。Payload 由 Kind 决定其具体类型。
type Event struct {
	Seq     Seq       `json:"seq"`
	Kind    Kind      `json:"kind"`
	Session string    `json:"session"`
	RunID   string    `json:"run_id,omitempty"`
	Time    time.Time `json:"time"`
	Payload any       `json:"payload,omitempty"`
}
