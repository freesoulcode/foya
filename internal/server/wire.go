package server

import (
	"github.com/freesoulcode/foya/internal/config"
	conversation "github.com/freesoulcode/foya/internal/conversation"
	interaction "github.com/freesoulcode/foya/internal/interaction"
	model "github.com/freesoulcode/foya/internal/model"
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

// SubmitTurnRequest starts a turn.
type SubmitTurnRequest struct {
	Session         string                        `json:"session"`
	Message         string                        `json:"message"`
	SkillRef        string                        `json:"skill_ref,omitempty"`
	Attachments     []conversation.AttachmentRef  `json:"attachments,omitempty"`
	BrowserElements []conversation.BrowserElement `json:"browser_elements,omitempty"`
}

type ArtifactResponse struct {
	Attachment conversation.AttachmentRef `json:"attachment"`
}

// CreateSessionRequest contains optional settings for a new chat.
type CreateSessionRequest struct {
	ConnectionID    string `json:"connection_id,omitempty"`
	Model           string `json:"model,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	ProjectID       string `json:"project_id,omitempty"`
	ApprovalMode    string `json:"approval_mode,omitempty"`
}

type ForkSessionRequest struct {
	Title      string `json:"title,omitempty"`
	ThroughSeq uint64 `json:"through_seq,omitempty"`
}

// UpdateSessionRequest partially updates chat settings.
type UpdateSessionRequest struct {
	ConnectionID    *string `json:"connection_id,omitempty"`
	Model           *string `json:"model,omitempty"`
	ReasoningEffort *string `json:"reasoning_effort,omitempty"`
	ProjectID       *string `json:"project_id,omitempty"`
	ApprovalMode    *string `json:"approval_mode,omitempty"`
	Title           *string `json:"title,omitempty"`  // Manual rename.
	Pinned          *bool   `json:"pinned,omitempty"` // Pin state.
}

// SubmitTurnResponse reports whether a message started or was queued.
type SubmitTurnResponse struct {
	RunID  string                      `json:"run_id,omitempty"`
	Status string                      `json:"status"` // started / queued
	Queued *conversation.QueuedMessage `json:"queued,omitempty"`
}

// RewindTurnRequest confirms moving one active user message back to the
// composer. ExpectedHeadSeq fences the preview the user confirmed.
type RewindTurnRequest struct {
	Confirm           bool     `json:"confirm"`
	ExpectedHeadSeq   uint64   `json:"expected_head_seq"`
	ExpectedFileState string   `json:"expected_file_state"`
	ForceFileKeys     []string `json:"force_file_keys,omitempty"`
}

type RewindFilePreview struct {
	Key       string `json:"key"`
	Path      string `json:"path"`
	Status    string `json:"status"` // ready / mergeable / modified
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Diff      string `json:"diff"`
}

// RewindTurnResponse either asks for confirmation or reports a completed rewind.
type RewindTurnResponse struct {
	Status         string              `json:"status"` // rewound / confirmation_required
	Message        string              `json:"message"`
	Files          []RewindFilePreview `json:"files,omitempty"`
	FileStateToken string              `json:"file_state_token"`
	HeadSeq        uint64              `json:"head_seq"`
}

type FileReviewResponse struct {
	Files          []RewindFilePreview `json:"files"`
	FileStateToken string              `json:"file_state_token"`
	ThroughSeq     uint64              `json:"through_seq"`
}

type ResolveFileReviewRequest struct {
	Action             string   `json:"action"` // keep / undo
	ExpectedThroughSeq uint64   `json:"expected_through_seq"`
	ExpectedFileState  string   `json:"expected_file_state"`
	ForceFileKeys      []string `json:"force_file_keys,omitempty"`
}

// QueueMessageRequest explicitly appends a queued message.
type QueueMessageRequest struct {
	Message         string                        `json:"message"`
	SkillRef        string                        `json:"skill_ref,omitempty"`
	Attachments     []conversation.AttachmentRef  `json:"attachments,omitempty"`
	BrowserElements []conversation.BrowserElement `json:"browser_elements,omitempty"`
}

// UpdateQueuedMessageRequest changes queued message content or position.
type UpdateQueuedMessageRequest struct {
	Message  *string `json:"message,omitempty"`
	Position *int    `json:"position,omitempty"`
}

// ConnectionConfig is the public, redacted Connection representation.
// APIKey is accepted only on writes; reads expose HasAPIKey instead.
type ConnectionConfig struct {
	ID            string                   `json:"id,omitempty"`
	Name          string                   `json:"name"`
	Type          string                   `json:"type"`
	VideoProtocol string                   `json:"video_protocol,omitempty"`
	Kind          string                   `json:"kind"`
	AuthKind      string                   `json:"auth_kind"`
	BaseURL       string                   `json:"base_url"`
	APIKey        string                   `json:"api_key,omitempty"`
	HasAPIKey     bool                     `json:"has_api_key,omitempty"`
	ModelSettings map[string]ModelSettings `json:"model_settings,omitempty"`
	Models        []string                 `json:"models,omitempty"`
	ModelsCached  bool                     `json:"models_cached,omitempty"`
	SortOrder     int                      `json:"sort_order"`
}

type DefaultModels = config.DefaultModels

type ModelSettings struct {
	ContextWindow    int64    `json:"context_window,omitempty"`
	MaxInputTokens   int64    `json:"max_input_tokens,omitempty"`
	MaxOutputTokens  int64    `json:"max_output_tokens,omitempty"`
	ImageInput       bool     `json:"image_input"`
	ImageGeneration  bool     `json:"image_generation"`
	VideoGeneration  bool     `json:"video_generation"`
	AudioGeneration  bool     `json:"audio_generation"`
	ToolCalling      bool     `json:"tool_calling"`
	WebSearch        bool     `json:"web_search"`
	ReasoningEfforts []string `json:"reasoning_efforts,omitempty"`
}

type ConnectionModelsResponse struct {
	Models         []string                           `json:"models"`
	ContextWindows map[string]int64                   `json:"context_windows,omitempty"`
	Capabilities   map[string]model.ModelCapabilities `json:"capabilities,omitempty"`
}

// ProviderConfig is the provider wire format. API keys are write-only.
type ProviderConfig struct {
	Kind      string `json:"kind"`
	BaseURL   string `json:"base_url"`
	Model     string `json:"model"`
	APIKey    string `json:"api_key,omitempty"`     // Write requests only.
	HasAPIKey bool   `json:"has_api_key,omitempty"` // Read responses only.
}

// ModelsResponse is returned by model catalog endpoints.
type ModelsResponse struct {
	Models         []string         `json:"models"`
	ContextWindows map[string]int64 `json:"context_windows,omitempty"`
}

// CompactSessionResponse describes a manual context compaction.
type CompactSessionResponse struct {
	CheckpointID          string `json:"checkpoint_id,omitempty"`
	Phase                 string `json:"phase,omitempty"`
	ProjectionKind        string `json:"projection_kind,omitempty"`
	Level                 string `json:"level,omitempty"`
	SegmentCount          int    `json:"segment_count,omitempty"`
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

// ApprovalDecisionRequest resolves an approval request.
type ApprovalDecisionRequest struct {
	RequestID string `json:"request_id"`
	Decision  string `json:"decision"`
}

// QuestionAnswerRequest resolves every question in one ask_user batch.
type QuestionAnswerRequest struct {
	Answers []interaction.Answer `json:"answers"`
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

type PluginInstallRequest struct {
	Source  string `json:"source"`
	Replace bool   `json:"replace,omitempty"`
}

type MarketplacePluginInstallRequest struct {
	Replace bool `json:"replace,omitempty"`
}

type MarketplaceAddRequest struct {
	Source      string   `json:"source"`
	Ref         string   `json:"ref,omitempty"`
	SparsePaths []string `json:"sparse_paths,omitempty"`
}

type MarketplaceEnabledRequest struct {
	Enabled bool `json:"enabled"`
}

type PluginEnabledRequest struct {
	Enabled bool `json:"enabled"`
}

type MCPPromptGetRequest struct {
	ServerID string            `json:"server_id"`
	Name     string            `json:"name"`
	Args     map[string]string `json:"args,omitempty"`
}

// SetCredentialRequest writes a secret outside the event stream.
type SetCredentialRequest struct {
	ConnectionID string `json:"connection_id"`
	Kind         string `json:"kind"`
	Secret       string `json:"secret"`
}

// ErrorResponse is the stable error envelope used by every REST endpoint.
type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
