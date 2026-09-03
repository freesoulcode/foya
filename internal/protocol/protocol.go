// Package protocol 定义客户端 ↔ 内核的线格式类型(REST + SSE)。
//
// 指令走 REST(Submission 方向),事件流走 SSE(Event 方向),靠 RunID
// 关联。SSE 事件带单调序号,支持 Last-Event-ID 断线补发。传输管道可换
// (本地 Unix socket / 远端 TCP+TLS),协议不变。
package protocol

import (
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/provider"
	"github.com/freesoulcode/foya/internal/question"
	"github.com/freesoulcode/foya/internal/queue"
)

type BrowserActionResultRequest struct {
	RequestID        string         `json:"request_id"`
	URL              string         `json:"url,omitempty"`
	Title            string         `json:"title,omitempty"`
	Revision         uint64         `json:"revision,omitempty"`
	ObservationID    string         `json:"observation_id,omitempty"`
	Snapshot         string         `json:"snapshot,omitempty"`
	ScreenshotBase64 string         `json:"screenshot_base64,omitempty"`
	MediaType        string         `json:"media_type,omitempty"`
	Code             string         `json:"code,omitempty"`
	Message          string         `json:"message,omitempty"`
	PreURL           string         `json:"pre_url,omitempty"`
	PostURL          string         `json:"post_url,omitempty"`
	Verified         *bool          `json:"verified,omitempty"`
	ActualText       string         `json:"actual_text,omitempty"`
	Trace            map[string]any `json:"trace,omitempty"`
	Error            string         `json:"error,omitempty"`
}

// SubmitTurnRequest 发起一个回合。
type SubmitTurnRequest struct {
	Session         string                   `json:"session"`
	Message         string                   `json:"message"`
	Attachments     []message.AttachmentRef  `json:"attachments,omitempty"`
	BrowserElements []message.BrowserElement `json:"browser_elements,omitempty"`
}

type ArtifactResponse struct {
	Attachment message.AttachmentRef `json:"attachment"`
}

// CreateSessionRequest 新建会话时的可选参数。
// 留空的字段由内核用当前 provider 默认值/默认审批档位填充。
type CreateSessionRequest struct {
	ConnectionID    string `json:"connection_id,omitempty"`
	Model           string `json:"model,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	ProjectID       string `json:"project_id,omitempty"`
	ApprovalMode    string `json:"approval_mode,omitempty"`
}

// UpdateSessionRequest 局部更新会话配置。
// ProjectID 只能首次绑定，已有非空值后不可更换或清空。
type UpdateSessionRequest struct {
	ConnectionID    *string `json:"connection_id,omitempty"`
	Model           *string `json:"model,omitempty"`
	ReasoningEffort *string `json:"reasoning_effort,omitempty"`
	ProjectID       *string `json:"project_id,omitempty"`
	ApprovalMode    *string `json:"approval_mode,omitempty"`
	Title           *string `json:"title,omitempty"`  // 手动改名;置 TitleIsManual=true
	Pinned          *bool   `json:"pinned,omitempty"` // 置顶/取消置顶
}

// SubmitTurnResponse 表示消息已直接启动或进入待发送队列。
type SubmitTurnResponse struct {
	RunID  string         `json:"run_id,omitempty"`
	Status string         `json:"status"` // started / queued
	Queued *queue.Message `json:"queued,omitempty"`
}

// EditTurnRequest replaces one active user turn. ConfirmEffects acknowledges
// that side effects from the superseded branch remain in the project tree.
type EditTurnRequest struct {
	Message         string `json:"message"`
	ConfirmEffects  bool   `json:"confirm_effects,omitempty"`
	ExpectedHeadSeq uint64 `json:"expected_head_seq,omitempty"`
}

// EditTurnResponse either reports a started replacement turn or asks the
// client to confirm retained side effects.
type EditTurnResponse struct {
	Status  string               `json:"status"` // started / confirmation_required
	Effects []event.BranchEffect `json:"effects,omitempty"`
	HeadSeq uint64               `json:"head_seq,omitempty"`
}

// QueueMessageRequest 显式向待发送队列追加消息。
type QueueMessageRequest struct {
	Message         string                   `json:"message"`
	Attachments     []message.AttachmentRef  `json:"attachments,omitempty"`
	BrowserElements []message.BrowserElement `json:"browser_elements,omitempty"`
}

// UpdateQueuedMessageRequest 修改队列消息正文或位置。
// Position 从 0 开始;省略字段表示保持不变。
type UpdateQueuedMessageRequest struct {
	Message  *string `json:"message,omitempty"`
	Position *int    `json:"position,omitempty"`
}

// ConnectionConfig is the public, redacted Connection representation.
// APIKey is accepted only on writes; reads expose HasAPIKey instead.
type ConnectionConfig struct {
	ID            string                   `json:"id,omitempty"`
	Name          string                   `json:"name"`
	Kind          string                   `json:"kind"`
	AuthKind      string                   `json:"auth_kind"`
	BaseURL       string                   `json:"base_url"`
	APIKey        string                   `json:"api_key,omitempty"`
	HasAPIKey     bool                     `json:"has_api_key,omitempty"`
	ContextWindow int64                    `json:"context_window,omitempty"`
	ModelSettings map[string]ModelSettings `json:"model_settings,omitempty"`
	SortOrder     int                      `json:"sort_order"`
}

type ModelSettings struct {
	ContextWindow    int64    `json:"context_window,omitempty"`
	ImageInput       bool     `json:"image_input"`
	ToolCalling      bool     `json:"tool_calling"`
	WebSearch        bool     `json:"web_search"`
	ReasoningEfforts []string `json:"reasoning_efforts,omitempty"`
}

type ConnectionModelsResponse struct {
	Models         []string                              `json:"models"`
	ContextWindows map[string]int64                      `json:"context_windows,omitempty"`
	Capabilities   map[string]provider.ModelCapabilities `json:"capabilities,omitempty"`
}

// ProviderConfig 是 provider 配置的线格式(读写设置界面用)。
// 读取时 APIKey 脱敏(仅返回是否已设置),写入时按需带上明文。
type ProviderConfig struct {
	Kind      string `json:"kind"`
	BaseURL   string `json:"base_url"`
	Model     string `json:"model"`
	APIKey    string `json:"api_key,omitempty"`     // 仅写入方向携带
	HasAPIKey bool   `json:"has_api_key,omitempty"` // 仅读取方向返回:是否已配置 key
}

// ModelsResponse 是 GET /config/models 的响应。
type ModelsResponse struct {
	Models         []string         `json:"models"`
	ContextWindows map[string]int64 `json:"context_windows,omitempty"`
}

// CompactSessionResponse 是一次手动上下文压缩的结果。
type CompactSessionResponse struct {
	ThroughSeq            uint64 `json:"through_seq"`
	EstimatedTokensBefore int64  `json:"estimated_tokens_before"`
	EstimatedTokensAfter  int64  `json:"estimated_tokens_after"`
}

// TerminalStartRequest supplies the viewport size before the shell emits its
// first prompt, keeping the PTY and terminal renderer in sync from byte one.
type TerminalStartRequest struct {
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// TerminalInputRequest forwards an input chunk to one terminal resource.
type TerminalInputRequest struct {
	Input string `json:"input"`
}

// TerminalResizeRequest updates terminal rows and columns.
type TerminalResizeRequest struct {
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// ApprovalDecisionRequest 是客户端回执一个审批决策。
type ApprovalDecisionRequest struct {
	RequestID string `json:"request_id"`
	Decision  string `json:"decision"`
}

// QuestionAnswerRequest resolves every question in one ask_user batch.
type QuestionAnswerRequest struct {
	Answers []question.Answer `json:"answers"`
}

type SkillEnableRequest struct {
	Enabled bool `json:"enabled"`
}

type SkillPinnedRequest struct {
	Pinned bool `json:"pinned"`
}

type ProjectCreateRequest struct {
	Name string `json:"name,omitempty"`
	Path string `json:"path"`
}

type ProjectUpdateRequest struct {
	Name   *string `json:"name,omitempty"`
	Pinned *bool   `json:"pinned,omitempty"`
}

type WebSearchTestRequest struct {
	ProviderID string `json:"provider_id"`
	Query      string `json:"query"`
}

type MCPResourceReadRequest struct {
	ServerID string `json:"server_id"`
	URI      string `json:"uri"`
}

type MCPPromptGetRequest struct {
	ServerID string            `json:"server_id"`
	Name     string            `json:"name"`
	Args     map[string]string `json:"args,omitempty"`
}

// SetCredentialRequest 配置凭证(独立安全通道,不进事件流)。
type SetCredentialRequest struct {
	ConnectionID string `json:"connection_id"`
	Kind         string `json:"kind"`
	Secret       string `json:"secret"`
}

// ErrorResponse 是统一错误返回。
type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
