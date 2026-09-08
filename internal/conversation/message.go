// Package message defines the core chat message types.
//
// A message is the atomic unit of chat history. History is projected from the
// event log, where each completed message is represented by MessageEnd.
package conversation

import (
	"strings"
	"time"

	"github.com/freesoulcode/foya/internal/model"
)

// Role identifies a message participant.
type Role = model.Role

const (
	RoleUser      = model.RoleUser
	RoleAssistant = model.RoleAssistant
	RoleSystem    = model.RoleSystem
	RoleTool      = model.RoleTool
)

// ToolCall is a tool invocation carried by an assistant message.
type ToolCall = model.ToolCall

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
	SkillRef        string           `json:"skill_ref,omitempty"`
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

// Message represents one chat message. Tool results reference their call
// through ToolCallID, while assistant messages may contain ToolCalls.
type Message struct {
	Role                 Role             `json:"role"`
	Content              string           `json:"content"`
	ModelContentOverride string           `json:"model_content,omitempty"`
	Command              string           `json:"command,omitempty"`
	SkillRef             string           `json:"skill_ref,omitempty"`
	Attachments          []AttachmentRef  `json:"attachments,omitempty"`
	BrowserElements      []BrowserElement `json:"browser_elements,omitempty"`
	EventSeq             uint64           `json:"event_seq,omitempty"` // Stable projection ID; not sent to the model.
	Reasoning            string           `json:"reasoning,omitempty"` // Display-only assistant reasoning.
	ToolCalls            []ToolCall       `json:"tool_calls,omitempty"`
	ToolCallID           string           `json:"tool_call_id,omitempty"`
	Diff                 string           `json:"diff,omitempty"` // Display-only file diff for tool results.
	FileChange           *FileChange      `json:"file_change,omitempty"`
	// Turn timestamps are stored only on the final assistant message.
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
	if m.Role == RoleUser && strings.TrimSpace(m.ModelContentOverride) != "" {
		return m.ModelContentOverride
	}
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
