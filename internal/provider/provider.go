// Package provider 抽象多个 LLM provider,屏蔽协议差异,统一流式接口。
//
// BYOK:拿用户自带的 key 直连 provider,token 流量不经任何第三方。
package provider

import (
	"context"
	"encoding/json"

	"github.com/freesoulcode/foya/internal/message"
)

// FunctionDef 是一个函数工具的定义。
type FunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

// ToolDef 是喂给模型的工具定义(OpenAI tool-calling 格式)。
type ToolDef struct {
	Type     string      `json:"type"` // 固定 "function"
	Function FunctionDef `json:"function"`
}

// StreamEvent 是模型流式响应的一个增量。
type StreamEvent struct {
	Type string // text_delta / reasoning_delta / tool_call_delta / done / error
	Text string

	// tool_call_delta 字段
	ToolIndex    int    // 该工具调用在本批次中的序号(用于分片拼接)
	ToolCallID   string // 首片携带
	ToolName     string // 首片携带
	ToolArgsDlt  string // 参数 JSON 的增量片段

	FinishReason string // stop / tool_calls / length / error(仅 done 事件)
}

// Request 是一次模型请求,携带完整对话历史(多轮上下文)与可用工具。
type Request struct {
	Model    string
	Messages []message.Message
	Tools    []ToolDef
}

// Provider 是统一的 LLM 接入点。
type Provider interface {
	Name() string
	// Stream 发起流式请求;实现必须把错误编码进事件流,不 panic。
	Stream(ctx context.Context, req Request) (<-chan StreamEvent, error)
}

// ModelLister 是可选能力:支持列出该 provider 上可用的模型。
// OpenAI 兼容服务通常通过 GET /models 返回模型列表;不支持的 provider 可不实现。
type ModelLister interface {
	ListModels(ctx context.Context) ([]string, error)
}

// Completer 是可选能力:非流式一次性生成短文本。
// 用于标题生成等旁路任务;不支持的 provider 可不实现,调用方走截断兜底。
type Completer interface {
	Complete(ctx context.Context, req Request) (string, error)
}
