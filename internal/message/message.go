// Package message 定义对话消息的基础类型。
//
// 消息是会话历史的原子单位:用户与助手的往返都是 Message。历史由事件
// 日志投影而来(每条完成的消息是一个 MessageEnd 事件),多轮对话即历史
// 的累积。
package message

import "encoding/json"

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

// Message 是一条对话消息。
// 纯文本消息:Role + Content。
// 助手调工具:Role=assistant, Content 可为空, ToolCalls 非空。
// 工具结果:Role=tool, ToolCallID 指向对应的调用, Content 为结果文本。
type Message struct {
	Role       Role       `json:"role"`
	Content    string     `json:"content"`
	Reasoning  string     `json:"reasoning,omitempty"` // 思考内容(仅 assistant),仅供展示,不回灌模型
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}
