// Package message 定义对话消息的基础类型。
//
// 消息是会话历史的原子单位:用户与助手的往返都是 Message。历史由事件
// 日志投影而来(每条完成的消息是一个 MessageEnd 事件),多轮对话即历史
// 的累积。
package message

import (
	"encoding/json"
	"strings"
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

// BrowserElement is a user-selected DOM element captured from the Workbar browser.
// It remains structured in history so the UI can render it without exposing the
// full HTML snapshot in the visible user message.
type BrowserElement struct {
	PageURL   string `json:"page_url"`
	PageTitle string `json:"page_title,omitempty"`
	Tag       string `json:"tag"`
	Selector  string `json:"selector"`
	Text      string `json:"text,omitempty"`
	HTML      string `json:"html,omitempty"`
}

// UserInput is the canonical input accepted by a turn or queued submission.
type UserInput struct {
	Text            string           `json:"text"`
	Command         string           `json:"command,omitempty"`
	Attachments     []AttachmentRef  `json:"attachments,omitempty"`
	BrowserElements []BrowserElement `json:"browser_elements,omitempty"`
}

// FileChange references the content-addressed states around one text-file write.
// Raw content is transient and stripped before the message enters the event log.
type FileChange struct {
	Path            string `json:"path"`
	BeforeExists    bool   `json:"before_exists"`
	BeforeMode      uint32 `json:"before_mode,omitempty"`
	AfterMode       uint32 `json:"after_mode,omitempty"`
	BeforeBlob      string `json:"before_blob,omitempty"`
	AfterBlob       string `json:"after_blob"`
	BeforeContent   []byte `json:"-"`
	AfterContent    []byte `json:"-"`
	ContentCaptured bool   `json:"-"`
}

// Message 是一条对话消息。
// 纯文本消息:Role + Content。
// 助手调工具:Role=assistant, Content 可为空, ToolCalls 非空。
// 工具结果:Role=tool, ToolCallID 指向对应的调用, Content 为结果文本。
type Message struct {
	Role            Role             `json:"role"`
	Content         string           `json:"content"`
	Command         string           `json:"command,omitempty"`
	Attachments     []AttachmentRef  `json:"attachments,omitempty"`
	BrowserElements []BrowserElement `json:"browser_elements,omitempty"`
	EventSeq        uint64           `json:"event_seq,omitempty"` // 历史投影中的稳定标识,不回灌模型
	Reasoning       string           `json:"reasoning,omitempty"` // 思考内容(仅 assistant),仅供展示,不回灌模型
	ToolCalls       []ToolCall       `json:"tool_calls,omitempty"`
	ToolCallID      string           `json:"tool_call_id,omitempty"`
	Diff            string           `json:"diff,omitempty"` // 文件变更 diff(仅 tool 结果),仅供 UI 展示,不回灌模型
	FileChange      *FileChange      `json:"file_change,omitempty"`
	// 回合生命周期时间仅写入最终 assistant 消息，不回灌模型。
	TurnStartedAt   *time.Time `json:"turn_started_at,omitempty"`
	TurnCompletedAt *time.Time `json:"turn_completed_at,omitempty"`
	TurnStatus      string     `json:"turn_status,omitempty"`
	TurnReason      string     `json:"turn_reason,omitempty"`
}

// ModelContent returns the provider-visible text for this message.
// Reasoning stays display-only for completed turns. If a turn was cancelled
// before a final answer, preserve the interrupted analysis as explicit context
// so a later user correction can build on that work.
func (m Message) ModelContent() string {
	content := strings.TrimSpace(m.Content)
	if m.Role != RoleAssistant || m.TurnStatus != "cancelled" {
		return m.Content
	}
	reasoning := strings.TrimSpace(m.Reasoning)
	if reasoning == "" {
		return m.Content
	}
	note := "[Interrupted assistant analysis preserved before cancellation]\n" + reasoning
	if content == "" {
		return note
	}
	return m.Content + "\n\n" + note
}

func (m Message) EmptyAssistantForModel() bool {
	return m.Role == RoleAssistant &&
		len(m.ToolCalls) == 0 &&
		strings.TrimSpace(m.ModelContent()) == ""
}
