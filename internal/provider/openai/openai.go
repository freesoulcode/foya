// Package openai implements an OpenAI-compatible streaming provider.
//
// BYOK requests connect directly to the configured endpoint.
//
// The official SDK handles wire formats and SSE. Non-standard fields such as
// reasoning_content are read through ExtraFields.
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

// Provider implements the OpenAI-compatible protocol.
type Provider struct {
	model         string
	contextWindow int64
	official      bool
	client        oai.Client
	mu            sync.RWMutex
	capabilities  map[string]provider.ModelCapabilities
}

// Config contains provider construction settings.
type Config struct {
	BaseURL       string
	APIKey        string
	Model         string
	ContextWindow int64
}

const defaultContextWindow int64 = 200_000

// New creates an OpenAI-compatible provider.
func New(cfg Config) *Provider {
	opts := []option.RequestOption{}
	// The SDK expects baseURL to point at a /v1 root.
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

// Name returns the provider identifier.
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

// Type conversion.

// toChatMsgs converts materialized messages to SDK parameters.
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

// toChatToolDefs converts normalized tools to SDK parameters.
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

// Streaming requests.

// Stream starts Chat Completions and converts chunks to StreamEvent values.
// Errors are reported through the stream. Non-standard reasoning_content is
// exposed as reasoning_delta events.
func (p *Provider) Stream(ctx context.Context, req provider.Request) (<-chan provider.StreamEvent, error) {
	model := req.Model
	if model == "" {
		model = p.model
	}
	if model == "" {
		return nil, fmt.Errorf("No model service is configured; set the Base URL, model, and API key in Settings")
	}

	params := oai.ChatCompletionNewParams{
		Model:         shared.ChatModel(model),
		Messages:      toChatMsgs(req.Messages),
		StreamOptions: oai.ChatCompletionStreamOptionsParam{IncludeUsage: oai.Bool(true)},
	}
	if req.MaxOutputTokens > 0 {
		params.MaxCompletionTokens = oai.Int(req.MaxOutputTokens)
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
				// Read non-standard reasoning_content through ExtraFields.
				if reasoning := extractReasoning(c.Delta); reasoning != "" {
					if !emit(provider.StreamEvent{Type: "reasoning_delta", Text: reasoning}) {
						return
					}
				}
				// Text delta.
				if c.Delta.Content != "" {
					if !emit(provider.StreamEvent{Type: "text_delta", Text: c.Delta.Content}) {
						return
					}
				}
				// Tool-call delta assembled by index in the engine.
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
			// The caller handles context cancellation through ctx.Err.
			ch <- provider.StreamEvent{Type: "error", Text: err.Error()}
			return
		}
		ch <- provider.StreamEvent{Type: "done", FinishReason: finishReason}
	}()

	return ch, nil
}

// extractReasoning reads the non-standard reasoning_content field used by
// providers such as vLLM, MLX, and Ollama.
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

// Complete performs a non-streaming request for short side tasks.
func (p *Provider) Complete(ctx context.Context, req provider.Request) (string, error) {
	completion, err := p.CompleteDetailed(ctx, req)
	if err != nil {
		return "", err
	}
	return completion.Text, nil
}

// CompleteDetailed returns text together with finish reason and usage.
func (p *Provider) CompleteDetailed(
	ctx context.Context,
	req provider.Request,
) (provider.Completion, error) {
	model := req.Model
	if model == "" {
		model = p.model
	}
	if model == "" {
		return provider.Completion{}, fmt.Errorf(
			"No model service is configured; set the Base URL, model, and API key in Settings",
		)
	}

	params := oai.ChatCompletionNewParams{
		Model:    shared.ChatModel(model),
		Messages: toChatMsgs(req.Messages),
	}
	if req.MaxOutputTokens > 0 {
		params.MaxCompletionTokens = oai.Int(req.MaxOutputTokens)
	}
	if req.ReasoningEffort != "" {
		params.ReasoningEffort = shared.ReasoningEffort(req.ReasoningEffort)
	}
	resp, err := p.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return provider.Completion{}, err
	}
	if len(resp.Choices) == 0 {
		return provider.Completion{}, nil
	}
	usage := &provider.Usage{
		Model:        model,
		InputTokens:  resp.Usage.PromptTokens,
		OutputTokens: resp.Usage.CompletionTokens,
		TotalTokens:  resp.Usage.TotalTokens,
		CachedTokens: resp.Usage.PromptTokensDetails.CachedTokens,
	}
	if usage.TotalTokens == 0 {
		usage = nil
	}
	return provider.Completion{
		Text:         resp.Choices[0].Message.Content,
		FinishReason: resp.Choices[0].FinishReason,
		Usage:        usage,
	}, nil
}

// ListModels returns model IDs and context windows from the configured endpoint.
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

// Compile-time interface checks.
var (
	_ provider.Provider           = (*Provider)(nil)
	_ provider.ModelLister        = (*Provider)(nil)
	_ provider.CapabilityResolver = (*Provider)(nil)
	_ provider.Completer          = (*Provider)(nil)
)
