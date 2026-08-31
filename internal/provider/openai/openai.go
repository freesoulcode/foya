// Package openai 是 OpenAI 兼容(Chat Completions)的流式 provider。
//
// BYOK:拿用户自带的 base_url + api_key 直连,token 流量不经任何第三方。
// 适配任何 OpenAI 兼容端点(官方、本地 vLLM/MLX/Ollama、各类网关)。
//
// 底层用官方 openai-go SDK 处理线格式、SSE 解析与工具调用分片拼接;
// 对于非标准扩展字段(如 vLLM/MLX 的 reasoning_content 思考内容),
// 通过 SDK 的 ExtraFields 机制读取。
package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"

	oai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/respjson"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"

	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/provider"
)

// Provider 是 OpenAI 兼容 provider。
type Provider struct {
	model         string
	contextWindow int64
	official      bool
	client        oai.Client
	mu            sync.RWMutex
	capabilities  map[string]provider.ModelCapabilities
}

// Config 是 provider 装配参数。
type Config struct {
	BaseURL       string
	APIKey        string
	Model         string
	ContextWindow int64
}

const defaultContextWindow int64 = 200_000

// New 创建一个 OpenAI 兼容 provider。
func New(cfg Config) *Provider {
	opts := []option.RequestOption{}
	// SDK 要求 baseURL 指向 /v1 根(如 http://127.0.0.1:8000/v1)。
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(strings.TrimRight(cfg.BaseURL, "/")+"/"))
	}
	if cfg.APIKey != "" {
		opts = append(opts, option.WithAPIKey(cfg.APIKey))
	}
	return &Provider{
		model:         cfg.Model,
		contextWindow: cfg.ContextWindow,
		official:      cfg.BaseURL == "" || strings.Contains(strings.ToLower(cfg.BaseURL), "api.openai.com"),
		client:        oai.NewClient(opts...),
		capabilities:  make(map[string]provider.ModelCapabilities),
	}
}

// Name 返回 provider 名。
func (p *Provider) Name() string { return "openai" }

func (p *Provider) ModelCapabilities(model string) provider.ModelCapabilities {
	p.mu.RLock()
	capabilities, ok := p.capabilities[model]
	p.mu.RUnlock()
	if ok {
		return capabilities
	}
	if !p.official {
		return provider.ModelCapabilities{}
	}
	value := strings.ToLower(model)
	supported := strings.HasPrefix(value, "gpt-4o") ||
		strings.HasPrefix(value, "gpt-4.1") ||
		strings.HasPrefix(value, "gpt-5") ||
		strings.HasPrefix(value, "o3") ||
		strings.HasPrefix(value, "o4")
	if !supported {
		return provider.ModelCapabilities{}
	}
	return provider.ModelCapabilities{ImageInput: boolPointer(true)}
}

func boolPointer(value bool) *bool { return &value }

// SearchWeb uses the Responses API hosted web-search tool. OpenAI-compatible
// endpoints that do not implement Responses return an error and the caller
// falls back to the configured external search provider.
func (p *Provider) SearchWeb(ctx context.Context, model, query string, limit int) ([]provider.SearchResult, error) {
	if model == "" {
		model = p.model
	}
	if model == "" || strings.TrimSpace(query) == "" {
		return nil, provider.ErrNativeSearchUnsupported
	}
	response, err := p.client.Responses.New(ctx, responses.ResponseNewParams{
		Model: shared.ResponsesModel(model),
		Input: responses.ResponseNewParamsInputUnion{OfString: oai.String(
			"Search the live web for the following query and answer using source citations: " + query,
		)},
		MaxToolCalls: oai.Int(4),
		Tools: []responses.ToolUnionParam{
			responses.ToolParamOfWebSearchPreview(responses.WebSearchToolTypeWebSearchPreview),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", provider.ErrNativeSearchUnsupported, err)
	}
	if limit <= 0 {
		limit = 5
	}
	if limit > 10 {
		limit = 10
	}
	snippet := response.OutputText()
	if len(snippet) > 500 {
		snippet = snippet[:500]
	}
	seen := make(map[string]bool)
	results := make([]provider.SearchResult, 0, limit)
	for _, item := range response.Output {
		for _, content := range item.Content {
			for _, annotation := range content.Annotations {
				if annotation.Type != "url_citation" {
					continue
				}
				citation := annotation.AsURLCitation()
				if citation.URL == "" || seen[citation.URL] {
					continue
				}
				seen[citation.URL] = true
				source := ""
				if location, parseErr := url.Parse(citation.URL); parseErr == nil {
					source = location.Hostname()
				}
				results = append(results, provider.SearchResult{
					Title: citation.Title, URL: citation.URL, Snippet: snippet,
					Source: source, Rank: len(results) + 1,
				})
				if len(results) >= limit {
					return results, nil
				}
			}
		}
	}
	if len(results) == 0 {
		return nil, provider.ErrNativeSearchUnsupported
	}
	return results, nil
}

// ---- 类型转换 ----

// toChatMsgs 把已物化的 Provider 消息转为 SDK 参数。
func toChatMsgs(msgs []provider.InputMessage) []oai.ChatCompletionMessageParamUnion {
	out := make([]oai.ChatCompletionMessageParamUnion, 0, len(msgs))
	for _, m := range msgs {
		text := inputText(m.Parts)
		switch m.Role {
		case message.RoleSystem:
			out = append(out, oai.SystemMessage(text))
		case message.RoleUser:
			if parts := userContentParts(m.Parts); len(parts) > 1 || hasImagePart(m.Parts) {
				out = append(out, oai.UserMessage(parts))
			} else {
				out = append(out, oai.UserMessage(text))
			}
		case message.RoleTool:
			out = append(out, oai.ToolMessage(text, m.ToolCallID))
			if hasImagePart(m.Parts) {
				parts := []oai.ChatCompletionContentPartUnionParam{{
					OfText: &oai.ChatCompletionContentPartTextParam{
						Text: "Image produced by tool call " + m.ToolCallID + ".",
					},
				}}
				parts = append(parts, imageContentParts(m.Parts)...)
				out = append(out, oai.UserMessage(parts))
			}
		case message.RoleAssistant:
			asst := oai.ChatCompletionAssistantMessageParam{}
			if text != "" {
				asst.Content.OfString = oai.String(text)
			}
			if len(m.ToolCalls) > 0 {
				calls := make([]oai.ChatCompletionMessageToolCallParam, 0, len(m.ToolCalls))
				for _, tc := range m.ToolCalls {
					calls = append(calls, oai.ChatCompletionMessageToolCallParam{
						ID: tc.ID,
						Function: oai.ChatCompletionMessageToolCallFunctionParam{
							Name:      tc.Name,
							Arguments: string(tc.Input),
						},
					})
				}
				asst.ToolCalls = calls
			}
			out = append(out, oai.ChatCompletionMessageParamUnion{OfAssistant: &asst})
		}
	}
	return out
}

func inputText(parts []provider.InputPart) string {
	var values []string
	for _, part := range parts {
		if part.Type == "text" && part.Text != "" {
			values = append(values, part.Text)
		}
	}
	return strings.Join(values, "\n")
}

func hasImagePart(parts []provider.InputPart) bool {
	for _, part := range parts {
		if part.Type == "image" && len(part.Data) > 0 && strings.HasPrefix(part.MediaType, "image/") {
			return true
		}
	}
	return false
}

func imageContentParts(parts []provider.InputPart) []oai.ChatCompletionContentPartUnionParam {
	var out []oai.ChatCompletionContentPartUnionParam
	for _, part := range parts {
		if part.Type != "image" || len(part.Data) == 0 || !strings.HasPrefix(part.MediaType, "image/") {
			continue
		}
		detail := part.Detail
		if detail == "" {
			detail = "auto"
		}
		out = append(out, oai.ChatCompletionContentPartUnionParam{
			OfImageURL: &oai.ChatCompletionContentPartImageParam{
				ImageURL: oai.ChatCompletionContentPartImageImageURLParam{
					URL:    "data:" + part.MediaType + ";base64," + base64.StdEncoding.EncodeToString(part.Data),
					Detail: detail,
				},
			},
		})
	}
	return out
}

func userContentParts(parts []provider.InputPart) []oai.ChatCompletionContentPartUnionParam {
	out := make([]oai.ChatCompletionContentPartUnionParam, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case "text":
			if part.Text != "" {
				out = append(out, oai.ChatCompletionContentPartUnionParam{
					OfText: &oai.ChatCompletionContentPartTextParam{Text: part.Text},
				})
			}
		case "image":
			out = append(out, imageContentParts([]provider.InputPart{part})...)
		}
	}
	return out
}

// toChatToolDefs 把 provider.ToolDef 转为 SDK 工具参数。
func toChatToolDefs(tools []provider.ToolDef) []oai.ChatCompletionToolParam {
	if len(tools) == 0 {
		return nil
	}
	out := make([]oai.ChatCompletionToolParam, 0, len(tools))
	for _, t := range tools {
		var params shared.FunctionParameters
		if len(t.Function.Parameters) > 0 {
			_ = json.Unmarshal(t.Function.Parameters, &params)
		}
		out = append(out, oai.ChatCompletionToolParam{
			Function: shared.FunctionDefinitionParam{
				Name:        t.Function.Name,
				Description: oai.String(t.Function.Description),
				Parameters:  params,
			},
		})
	}
	return out
}

// ---- 流式请求 ----

// Stream 发起流式 Chat Completions 请求,把增量编码为 StreamEvent。
// 错误一律编码进事件流,不 panic。
// 除标准的 content / tool_calls 外,还提取非标准的 reasoning_content
// (vLLM/MLX/Ollama 思考内容扩展),编码为 reasoning_delta 事件。
func (p *Provider) Stream(ctx context.Context, req provider.Request) (<-chan provider.StreamEvent, error) {
	model := req.Model
	if model == "" {
		model = p.model
	}
	if model == "" {
		return nil, fmt.Errorf("尚未配置模型服务,请在「设置」中填写 Base URL、模型和 API Key")
	}

	params := oai.ChatCompletionNewParams{
		Model:         shared.ChatModel(model),
		Messages:      toChatMsgs(req.Messages),
		StreamOptions: oai.ChatCompletionStreamOptionsParam{IncludeUsage: oai.Bool(true)},
	}
	if req.ReasoningEffort != "" {
		params.ReasoningEffort = shared.ReasoningEffort(req.ReasoningEffort)
	}
	if tools := toChatToolDefs(req.Tools); len(tools) > 0 {
		params.Tools = tools
	}

	stream := p.client.Chat.Completions.NewStreaming(ctx, params)

	ch := make(chan provider.StreamEvent)
	go func() {
		defer close(ch)

		var finishReason string
		emit := func(ev provider.StreamEvent) bool {
			select {
			case <-ctx.Done():
				return false
			case ch <- ev:
				return true
			}
		}

		for stream.Next() {
			chunk := stream.Current()
			if chunk.JSON.Usage.Valid() && chunk.Usage.TotalTokens > 0 {
				usage := provider.Usage{
					Model:        model,
					InputTokens:  chunk.Usage.PromptTokens,
					OutputTokens: chunk.Usage.CompletionTokens,
					TotalTokens:  chunk.Usage.TotalTokens,
					CachedTokens: chunk.Usage.PromptTokensDetails.CachedTokens,
				}
				if !emit(provider.StreamEvent{Type: "usage", Usage: &usage}) {
					return
				}
			}
			for _, c := range chunk.Choices {
				// 非标准思考内容(reasoning_content):走 ExtraFields 提取。
				if reasoning := extractReasoning(c.Delta); reasoning != "" {
					if !emit(provider.StreamEvent{Type: "reasoning_delta", Text: reasoning}) {
						return
					}
				}
				// 文本增量
				if c.Delta.Content != "" {
					if !emit(provider.StreamEvent{Type: "text_delta", Text: c.Delta.Content}) {
						return
					}
				}
				// 工具调用增量(分片,按 Index 拼接由 engine 层完成)
				for _, tc := range c.Delta.ToolCalls {
					if !emit(provider.StreamEvent{
						Type:        "tool_call_delta",
						ToolIndex:   int(tc.Index),
						ToolCallID:  tc.ID,
						ToolName:    tc.Function.Name,
						ToolArgsDlt: tc.Function.Arguments,
					}) {
						return
					}
				}
				if c.FinishReason != "" {
					finishReason = c.FinishReason
				}
			}
		}
		if err := stream.Err(); err != nil {
			// ctx 取消:交由上层按 ctx.Err() 静默处理。
			ch <- provider.StreamEvent{Type: "error", Text: err.Error()}
			return
		}
		ch <- provider.StreamEvent{Type: "done", FinishReason: finishReason}
	}()

	return ch, nil
}

// extractReasoning 从流片 delta 中提取非标准的 reasoning_content 字段。
// 该字段不属于 OpenAI 官方协议,由 vLLM/MLX/Ollama 等在思考模型上返回。
// 注意:未建模字段落在 ExtraFields 中,其 respjson.Field.Valid() 对扩展字段
// 恒为 false(SDK 不为其做类型校验),故只能据 Raw() 是否为空来判断存在性。
func extractReasoning(delta oai.ChatCompletionChunkChoiceDelta) string {
	f, ok := delta.JSON.ExtraFields["reasoning_content"]
	if !ok {
		return ""
	}
	raw := f.Raw()
	if raw == "" || raw == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return ""
	}
	return s
}

// Complete 发起非流式 Chat Completions 请求,返回完整文本。
// 用于标题生成等一次性短文本旁路任务。不携带工具定义。
func (p *Provider) Complete(ctx context.Context, req provider.Request) (string, error) {
	model := req.Model
	if model == "" {
		model = p.model
	}
	if model == "" {
		return "", fmt.Errorf("尚未配置模型服务,请在「设置」中填写 Base URL、模型和 API Key")
	}

	params := oai.ChatCompletionNewParams{
		Model:    shared.ChatModel(model),
		Messages: toChatMsgs(req.Messages),
	}
	if req.ReasoningEffort != "" {
		params.ReasoningEffort = shared.ReasoningEffort(req.ReasoningEffort)
	}
	resp, err := p.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", nil
	}
	return resp.Choices[0].Message.Content, nil
}

// ListModels 请求 OpenAI 兼容的 /models 接口,返回模型 ID 与上下文窗口。
// 复用已配置的 baseURL 与 api_key,直连用户自带端点(BYOK)。
func (p *Provider) ListModels(ctx context.Context) ([]provider.ModelInfo, error) {
	page, err := p.client.Models.List(ctx)
	if err != nil {
		return nil, err
	}
	models := make([]provider.ModelInfo, 0, len(page.Data))
	capabilitiesByModel := make(map[string]provider.ModelCapabilities, len(page.Data))
	for _, m := range page.Data {
		if m.ID != "" {
			contextWindow := effectiveContextWindow(
				extractContextWindow(m.JSON.ExtraFields),
				p.contextWindow,
			)
			capabilities := p.ModelCapabilities(m.ID)
			if imageInput, ok := extractImageInput(m.JSON.ExtraFields); ok {
				capabilities.ImageInput = boolPointer(imageInput)
			}
			models = append(models, provider.ModelInfo{
				ID:            m.ID,
				ContextWindow: contextWindow,
				Capabilities:  capabilities,
			})
			capabilitiesByModel[m.ID] = capabilities
		}
	}
	p.mu.Lock()
	p.capabilities = capabilitiesByModel
	p.mu.Unlock()
	return models, nil
}

func extractImageInput(fields map[string]respjson.Field) (bool, bool) {
	for _, key := range []string{"supports_image_input", "supports_vision"} {
		field, ok := fields[key]
		if !ok || field.Raw() == "" {
			continue
		}
		var value bool
		if json.Unmarshal([]byte(field.Raw()), &value) == nil {
			return value, true
		}
	}
	field, ok := fields["input_modalities"]
	if !ok || field.Raw() == "" {
		return false, false
	}
	var modalities []string
	if json.Unmarshal([]byte(field.Raw()), &modalities) != nil {
		return false, false
	}
	for _, modality := range modalities {
		if strings.EqualFold(modality, "image") {
			return true, true
		}
	}
	return false, true
}

func effectiveContextWindow(reported, configured int64) int64 {
	if reported > 0 {
		return reported
	}
	if configured > 0 {
		return configured
	}
	return defaultContextWindow
}

func extractContextWindow(fields map[string]respjson.Field) int64 {
	for _, key := range []string{
		"max_model_len",
		"context_window",
		"context_length",
		"max_context_length",
	} {
		field, ok := fields[key]
		if !ok {
			continue
		}
		raw := field.Raw()
		var integer int64
		if err := json.Unmarshal([]byte(raw), &integer); err == nil && integer > 0 {
			return integer
		}
		var text string
		if err := json.Unmarshal([]byte(raw), &text); err == nil {
			if parsed, err := strconv.ParseInt(text, 10, 64); err == nil && parsed > 0 {
				return parsed
			}
		}
	}
	return 0
}

// 确保实现了接口。
var (
	_ provider.Provider           = (*Provider)(nil)
	_ provider.ModelLister        = (*Provider)(nil)
	_ provider.CapabilityResolver = (*Provider)(nil)
	_ provider.Completer          = (*Provider)(nil)
)
