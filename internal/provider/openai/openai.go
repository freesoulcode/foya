// Package openai 是 OpenAI 兼容(Chat Completions)的流式 provider。
//
// BYOK:拿用户自带的 base_url + api_key 直连,token 流量不经任何第三方。
// 适配任何 OpenAI 兼容端点(官方、本地 vLLM/MLX/Ollama、各类网关)。
package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/freesoulcode/foya/internal/provider"
)

// Provider 是 OpenAI 兼容 provider。
type Provider struct {
	baseURL string // 形如 http://127.0.0.1:8000/v1
	apiKey  string
	model   string
	client  *http.Client
}

// Config 是 provider 装配参数。
type Config struct {
	BaseURL string
	APIKey  string
	Model   string
}

// New 创建一个 OpenAI 兼容 provider。
func New(cfg Config) *Provider {
	return &Provider{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:  cfg.APIKey,
		model:   cfg.Model,
		client:  &http.Client{}, // 流式请求不设整体超时,靠 ctx 取消
	}
}

// Name 返回 provider 名。
func (p *Provider) Name() string { return "openai" }

// chatRequest 是 Chat Completions 请求体(仅用到的字段)。
type chatRequest struct {
	Model    string    `json:"model"`
	Messages []chatMsg `json:"messages"`
	Stream   bool      `json:"stream"`
}

type chatMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatChunk 是流式响应的一个 SSE data 块(仅用到的字段)。
type chatChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
}

// chatResponse 是非流式响应(仅用到的字段)。
type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// Stream 发起流式 Chat Completions 请求,把增量编码为 StreamEvent。
// 错误一律编码进事件流,不 panic。
func (p *Provider) Stream(ctx context.Context, req provider.Request) (<-chan provider.StreamEvent, error) {
	if p.baseURL == "" {
		return nil, fmt.Errorf("尚未配置模型服务,请在「设置」中填写 Base URL、模型和 API Key")
	}

	model := req.Model
	if model == "" {
		model = p.model
	}

	msgs := make([]chatMsg, 0, len(req.Messages))
	for _, m := range req.Messages {
		msgs = append(msgs, chatMsg{Role: string(m.Role), Content: m.Content})
	}

	body, err := json.Marshal(chatRequest{Model: model, Messages: msgs, Stream: true})
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	if p.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(resp.Body)
		return nil, fmt.Errorf("provider 返回 %s: %s", resp.Status, strings.TrimSpace(buf.String()))
	}

	ch := make(chan provider.StreamEvent)
	go func() {
		defer close(ch)
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			data, ok := strings.CutPrefix(line, "data: ")
			if !ok {
				continue // 跳过空行、注释、事件名行
			}
			if data == "[DONE]" {
				break
			}

			var chunk chatChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				continue // 容错:忽略无法解析的块
			}
			for _, c := range chunk.Choices {
				if c.Delta.Content != "" {
					select {
					case <-ctx.Done():
						ch <- provider.StreamEvent{Type: "error", Text: ctx.Err().Error()}
						return
					case ch <- provider.StreamEvent{Type: "text_delta", Text: c.Delta.Content}:
					}
				}
			}
		}
		if err := scanner.Err(); err != nil {
			ch <- provider.StreamEvent{Type: "error", Text: err.Error()}
			return
		}
		ch <- provider.StreamEvent{Type: "done"}
	}()

	return ch, nil
}

// Complete 发起非流式 Chat Completions 请求,返回完整文本。
// 用于标题生成等一次性短文本旁路任务。
func (p *Provider) Complete(ctx context.Context, req provider.Request) (string, error) {
	if p.baseURL == "" {
		return "", fmt.Errorf("尚未配置模型服务,请在「设置」中填写 Base URL、模型和 API Key")
	}

	model := req.Model
	if model == "" {
		model = p.model
	}

	msgs := make([]chatMsg, 0, len(req.Messages))
	for _, m := range req.Messages {
		msgs = append(msgs, chatMsg{Role: string(m.Role), Content: m.Content})
	}

	body, err := json.Marshal(chatRequest{Model: model, Messages: msgs, Stream: false})
	if err != nil {
		return "", err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(resp.Body)
		return "", fmt.Errorf("provider 返回 %s: %s", resp.Status, strings.TrimSpace(buf.String()))
	}

	var out chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", nil
	}
	return out.Choices[0].Message.Content, nil
}

// modelsResponse 是 GET /models 的响应(仅取需要的字段)。
type modelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// ListModels 请求 OpenAI 兼容的 /models 接口,返回模型 ID 列表。
// 复用已配置的 baseURL 与 apiKey,直连用户自带端点(BYOK)。
func (p *Provider) ListModels(ctx context.Context) ([]string, error) {
	if p.baseURL == "" {
		return nil, fmt.Errorf("尚未配置模型服务,请在「设置」中填写 Base URL 和 API Key")
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	if p.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(resp.Body)
		return nil, fmt.Errorf("provider 返回 %s: %s", resp.Status, strings.TrimSpace(buf.String()))
	}

	var mr modelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&mr); err != nil {
		return nil, fmt.Errorf("解析模型列表失败: %w", err)
	}
	ids := make([]string, 0, len(mr.Data))
	for _, m := range mr.Data {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	return ids, nil
}

// 确保实现了接口。
var (
	_ provider.Provider    = (*Provider)(nil)
	_ provider.ModelLister = (*Provider)(nil)
	_ provider.Completer   = (*Provider)(nil)
)
