// Package provider 抽象多个 LLM provider,屏蔽协议差异,统一流式接口。
//
// BYOK:拿用户自带的 key 直连 provider,token 流量不经任何第三方。
// 脚手架阶段先提供 mock/echo 实现,真实 provider 后置接入。
package provider

import "context"

// StreamEvent 是模型流式响应的一个增量。
type StreamEvent struct {
	Type string // text_delta / tool_call / done / error
	Text string
}

// Request 是一次模型请求(占位,后续替换为结构化上下文)。
type Request struct {
	Model    string
	System   string
	Messages []string
}

// Provider 是统一的 LLM 接入点。
type Provider interface {
	Name() string
	// Stream 发起流式请求;实现必须把错误编码进事件流,不 panic。
	Stream(ctx context.Context, req Request) (<-chan StreamEvent, error)
}
