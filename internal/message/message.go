// Package message 定义对话消息的基础类型。
//
// 消息是会话历史的原子单位:用户与助手的往返都是 Message。历史由事件
// 日志投影而来(每条完成的消息是一个 MessageEnd 事件),多轮对话即历史
// 的累积。
package message

import (
	"encoding/json"
	"time"
)

// Role 是消息角色。
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
	RoleTool      Role = "tool"
)

// ToolCall 是助手发起的一次工具调用(assistant 消息携带)。
type ToolCall struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// AttachmentRef points at bytes owned by the session artifact store. Binary
// content never enters the event log or SSE stream.
type AttachmentRef struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	MediaType string `json:"media_type"`
	Bytes     int64  `json:"bytes"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
}

// UserInput is the canonical input accepted by a turn or queued submission.
type UserInput struct {
	Text        string          `json:"text"`
	Attachments []AttachmentRef `json:"attachments,omitempty"`
}

// Message 是一条对话消息。
// 纯文本消息:Role + Content。
// 助手调工具:Role=assistant, Content 可为空, ToolCalls 非空。
// 工具结果:Role=tool, ToolCallID 指向对应的调用, Content 为结果文本。
type Message struct {
	Role        Role            `json:"role"`
	Content     string          `json:"content"`
	Attachments []AttachmentRef `json:"attachments,omitempty"`
	EventSeq    uint64          `json:"event_seq,omitempty"` // 历史投影中的稳定标识,不回灌模型
	Reasoning   string          `json:"reasoning,omitempty"` // 思考内容(仅 assistant),仅供展示,不回灌模型
	ToolCalls   []ToolCall      `json:"tool_calls,omitempty"`
	ToolCallID  string          `json:"tool_call_id,omitempty"`
	Diff        string          `json:"diff,omitempty"` // 文件变更 diff(仅 tool 结果),仅供展示,不回灌模型
	// 回合生命周期时间仅写入最终 assistant 消息，不回灌模型。
	TurnStartedAt   *time.Time `json:"turn_started_at,omitempty"`
	TurnCompletedAt *time.Time `json:"turn_completed_at,omitempty"`
}
