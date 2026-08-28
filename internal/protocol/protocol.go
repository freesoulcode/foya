// Package protocol 定义客户端 ↔ 内核的线格式类型(REST + SSE)。
//
// 指令走 REST(Submission 方向),事件流走 SSE(Event 方向),靠 RunID
// 关联。SSE 事件带单调序号,支持 Last-Event-ID 断线补发。传输管道可换
// (本地 Unix socket / 远端 TCP+TLS),协议不变。
package protocol

import (
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/queue"
)

// SubmitTurnRequest 发起一个回合。
type SubmitTurnRequest struct {
	Session string `json:"session"`
	Message string `json:"message"`
}

// CreateSessionRequest 新建会话时的可选参数。
// 留空的字段由内核用当前 provider 默认值/默认审批档位填充。
type CreateSessionRequest struct {
	Model        string `json:"model,omitempty"`
	Workspace    string `json:"workspace,omitempty"`
	ApprovalMode string `json:"approval_mode,omitempty"`
}

// UpdateSessionRequest 局部更新会话可变字段。
// 指针为 nil 表示不变;空字符串表示清空。
type UpdateSessionRequest struct {
	Model        *string `json:"model,omitempty"`
	Workspace    *string `json:"workspace,omitempty"`
	ApprovalMode *string `json:"approval_mode,omitempty"`
	Title        *string `json:"title,omitempty"`  // 手动改名;置 TitleIsManual=true
	Pinned       *bool   `json:"pinned,omitempty"` // 置顶/取消置顶
}

// SubmitTurnResponse 表示消息已直接启动或进入待发送队列。
type SubmitTurnResponse struct {
	RunID  string         `json:"run_id,omitempty"`
	Status string         `json:"status"` // started / queued
	Queued *queue.Message `json:"queued,omitempty"`
}

// EditTurnRequest replaces one active user turn. ConfirmEffects acknowledges
// that side effects from the superseded branch remain in the workspace.
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
	Message string `json:"message"`
}

// UpdateQueuedMessageRequest 修改队列消息正文或位置。
// Position 从 0 开始;省略字段表示保持不变。
type UpdateQueuedMessageRequest struct {
	Message  *string `json:"message,omitempty"`
	Position *int    `json:"position,omitempty"`
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

// ApprovalDecisionRequest 是客户端回执一个审批决策。
type ApprovalDecisionRequest struct {
	RequestID string `json:"request_id"`
	Decision  string `json:"decision"`
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
