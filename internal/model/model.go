// Package model defines the provider-neutral model runtime contract.
//
// BYOK requests connect directly to providers without proxying token traffic.
package model

import (
	"context"
	"encoding/json"
	"errors"
)

var (
	ErrNativeSearchUnsupported     = errors.New("provider native web search is unsupported")
	ErrNativeCompactionUnsupported = errors.New("provider native compaction is unsupported")
)

// Role identifies a model conversation participant.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
	RoleTool      Role = "tool"
)

// ToolCall is a model-requested function invocation.
type ToolCall struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// FunctionDef describes a callable function tool.
type FunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

// ToolDef is the normalized tool definition sent to a model.
type ToolDef struct {
	Type     string      `json:"type"` // Always "function".
	Function FunctionDef `json:"function"`
}

// Usage contains token counts for one model request.
// CachedTokens is a subset of InputTokens and still consumes context.
type Usage struct {
	Model        string `json:"model"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	TotalTokens  int64  `json:"total_tokens"`
	CachedTokens int64  `json:"cached_tokens"`
}

// ModelInfo contains catalog metadata. A zero ContextWindow means unknown.
type ModelInfo struct {
	ID            string            `json:"id"`
	ContextWindow int64             `json:"context_window,omitempty"`
	Capabilities  ModelCapabilities `json:"capabilities,omitempty"`
}

// ModelCapabilities describes model input features. Nil means the provider did
// not advertise the capability, rather than explicitly rejecting it.
type ModelCapabilities struct {
	ImageInput *bool `json:"image_input,omitempty"`
}

// InputPart is provider-ready content. Image bytes exist only while materializing
// a request and are never persisted in the event log.
type InputPart struct {
	Type      string
	Text      string
	Data      []byte
	MediaType string
	Detail    string
}

// InputMessage is the provider-facing projection of one canonical message.
type InputMessage struct {
	Role       Role
	Parts      []InputPart
	ToolCalls  []ToolCall
	ToolCallID string
}

// TextMessage constructs a model input containing one text part.
func TextMessage(role Role, content string) InputMessage {
	return InputMessage{
		Role:  role,
		Parts: []InputPart{{Type: "text", Text: content}},
	}
}

// SearchResult is a provider-neutral web search result.
type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
	Source  string `json:"source,omitempty"`
	Rank    int    `json:"rank"`
}

// NativeWebSearcher is an optional provider capability.
type NativeWebSearcher interface {
	SearchWeb(ctx context.Context, model, query string, limit int) ([]SearchResult, error)
}

// StreamEvent is one model response stream delta.
type StreamEvent struct {
	Type  string // text_delta / reasoning_delta / tool_call_delta / done / error
	Text  string
	Usage *Usage

	// Tool-call delta fields.
	ToolIndex   int    // Index within the current tool-call batch.
	ToolCallID  string // Present on the first delta.
	ToolName    string // Present on the first delta.
	ToolArgsDlt string // JSON argument fragment.

	FinishReason string // stop / tool_calls / length / error on done events.
}

// Request contains full conversation context and available tools.
type Request struct {
	Model           string
	ReasoningEffort string
	MaxOutputTokens int64
	Messages        []InputMessage
	Tools           []ToolDef
	ContextState    *ContextState
}

// ContextState is an opaque provider-native continuation state.
type ContextState struct {
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data"`
}

// Provider is the normalized LLM entry point.
type Provider interface {
	Name() string
	// Stream starts a request and reports failures through the event stream.
	Stream(ctx context.Context, req Request) (<-chan StreamEvent, error)
}

// ModelLister is an optional capability for listing available models.
type ModelLister interface {
	ListModels(ctx context.Context) ([]ModelInfo, error)
}

// CapabilityResolver reports capabilities that cannot be reliably inferred
// from the generic model-list endpoint.
type CapabilityResolver interface {
	ModelCapabilities(model string) ModelCapabilities
}

// Completer is an optional non-streaming capability for short side tasks.
type Completer interface {
	Complete(ctx context.Context, req Request) (string, error)
}

// Completion is the detailed result of a short non-streaming generation.
type Completion struct {
	Text         string
	FinishReason string
	Usage        *Usage
}

// DetailedCompleter is an optional extension for callers that must reject
// truncated output and account for the physical model request.
type DetailedCompleter interface {
	CompleteDetailed(ctx context.Context, req Request) (Completion, error)
}

type NativeCompactionResult struct {
	State ContextState
	Usage *Usage
}

// NativeContextCompactor is implemented only by providers whose request
// protocol can both create and replay an opaque compaction state.
type NativeContextCompactor interface {
	CompactContext(ctx context.Context, req Request) (NativeCompactionResult, error)
}
