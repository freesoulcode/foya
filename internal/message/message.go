// Package message 定义对话消息的基础类型。
//
// 消息是会话历史的原子单位:用户与助手的往返都是 Message。历史由事件
// 日志投影而来(每条完成的消息是一个 MessageEnd 事件),多轮对话即历史
// 的累积。
package message

// Role 是消息角色。
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
)

// Message 是一条对话消息。
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
}
