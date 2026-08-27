// Package protocol 定义客户端 ↔ 内核的线格式类型(REST + SSE)。
//
// 指令走 REST(Submission 方向),事件流走 SSE(Event 方向),靠 RunID
// 关联。SSE 事件带单调序号,支持 Last-Event-ID 断线补发。传输管道可换
// (本地 Unix socket / 远端 TCP+TLS),协议不变。
package protocol

// SubmitTurnRequest 发起一个回合。
type SubmitTurnRequest struct {
	Session string `json:"session"`
	Message string `json:"message"`
}

// SubmitTurnResponse 返回该回合的 RunID,用于在 SSE 流中关联事件。
type SubmitTurnResponse struct {
	RunID string `json:"run_id"`
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
