// Package provider 抽象多个 LLM provider,屏蔽协议差异,统一流式接口。
//
// BYOK:拿用户自带的 key 直连 provider,token 流量不经任何第三方。
package provider

import (
	"context"

	"github.com/freesoulcode/foya/internal/message"
)

// StreamEvent 是模型流式响应的一个增量。
type StreamEvent struct {
	Type string // text_delta / done / error
	Text string
}

// Request 是一次模型请求,携带完整对话历史(多轮上下文)。
type Request struct {
	Model    string
	Messages []message.Message
}

// Provider 是统一的 LLM 接入点。
type Provider interface {
	Name() string
	// Stream 发起流式请求;实现必须把错误编码进事件流,不 panic。
	Stream(ctx context.Context, req Request) (<-chan StreamEvent, error)
}
