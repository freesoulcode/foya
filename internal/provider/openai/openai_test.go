package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	oai "github.com/openai/openai-go"

	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/provider"
)

func TestToChatMsgsEncodesUserAndToolImages(t *testing.T) {
	imageData := []byte{0x89, 'P', 'N', 'G'}
	converted := toChatMsgs([]provider.InputMessage{
		{
			Role: message.RoleUser,
			Parts: []provider.InputPart{
				{Type: "text", Text: "describe this"},
				{Type: "image", Data: imageData, MediaType: "image/png"},
			},
		},
		{
			Role:       message.RoleTool,
			ToolCallID: "call-1",
			Parts: []provider.InputPart{
				{Type: "text", Text: "[Image: screenshot.png]"},
				{Type: "image", Data: imageData, MediaType: "image/png"},
			},
		},
	})
	data, err := json.Marshal(converted)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range [][]byte{
		[]byte(`"role":"user"`),
		[]byte(`"role":"tool"`),
		[]byte(`"tool_call_id":"call-1"`),
		[]byte(`"type":"image_url"`),
		[]byte(`"url":"data:image/png;base64,iVBORw=="`),
		[]byte(`"detail":"auto"`),
		[]byte(`Image produced by tool call call-1.`),
	} {
		if !bytes.Contains(data, fragment) {
			t.Fatalf("serialized messages missing %s: %s", fragment, data)
		}
	}
	if count := bytes.Count(data, []byte(`"type":"image_url"`)); count != 2 {
		t.Fatalf("image part count = %d, want 2: %s", count, data)
	}
}

func TestExtractContextWindow(t *testing.T) {
	var model oai.Model
	if err := json.Unmarshal([]byte(`{
		"id": "test-model",
		"object": "model",
		"created": 1,
		"owned_by": "test",
		"max_model_len": 40960
	}`), &model); err != nil {
		t.Fatal(err)
	}
	if got := extractContextWindow(model.JSON.ExtraFields); got != 40960 {
		t.Fatalf("context window = %d, want 40960", got)
	}
}

func TestEffectiveContextWindowPrecedence(t *testing.T) {
	if got := effectiveContextWindow(128_000, 64_000); got != 128_000 {
		t.Fatalf("reported context window = %d", got)
	}
	if got := effectiveContextWindow(0, 64_000); got != 64_000 {
		t.Fatalf("configured context window = %d", got)
	}
	if got := effectiveContextWindow(0, 0); got != 200_000 {
		t.Fatalf("default context window = %d", got)
	}
}

func TestListModelsCachesAdvertisedImageCapabilities(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"object":"list","data":[
			{"id":"vision-model","object":"model","created":1,"owned_by":"test","input_modalities":["text","image"]},
			{"id":"text-model","object":"model","created":1,"owned_by":"test","supports_vision":false}
		]}`)
	}))
	defer server.Close()

	p := New(Config{BaseURL: server.URL + "/v1", APIKey: "test"})
	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].Capabilities.ImageInput == nil ||
		!*models[0].Capabilities.ImageInput || models[1].Capabilities.ImageInput == nil ||
		*models[1].Capabilities.ImageInput {
		t.Fatalf("unexpected model capabilities: %#v", models)
	}
	cached := p.ModelCapabilities("text-model")
	if cached.ImageInput == nil || *cached.ImageInput {
		t.Fatalf("cached capabilities = %#v", cached)
	}
}

func TestStreamRequestsAndEmitsUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var body struct {
			StreamOptions struct {
				IncludeUsage bool `json:"include_usage"`
			} `json:"stream_options"`
			ReasoningEffort     string `json:"reasoning_effort"`
			MaxCompletionTokens int64  `json:"max_completion_tokens"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if !body.StreamOptions.IncludeUsage {
			t.Error("stream_options.include_usage was not enabled")
		}
		if body.ReasoningEffort != "high" {
			t.Fatalf("reasoning_effort = %q, want high", body.ReasoningEffort)
		}
		if body.MaxCompletionTokens != 512 {
			t.Fatalf("max_completion_tokens = %d, want 512", body.MaxCompletionTokens)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"id":"test","object":"chat.completion.chunk","created":1,"model":"test-model","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}],"usage":null}`)
		fmt.Fprintln(w)
		fmt.Fprintln(w, `data: {"id":"test","object":"chat.completion.chunk","created":1,"model":"test-model","choices":[],"usage":{"prompt_tokens":100,"completion_tokens":5,"total_tokens":105,"prompt_tokens_details":{"cached_tokens":80}}}`)
		fmt.Fprintln(w)
		fmt.Fprintln(w, "data: [DONE]")
	}))
	defer server.Close()

	p := New(Config{BaseURL: server.URL + "/v1", APIKey: "test", Model: "test-model"})
	stream, err := p.Stream(context.Background(), provider.Request{
		ReasoningEffort: "high",
		MaxOutputTokens: 512,
		Messages: []provider.InputMessage{
			provider.TextMessage(message.Message{Role: message.RoleUser, Content: "hello"}),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	var usage *provider.Usage
	for event := range stream {
		if event.Usage != nil {
			usage = event.Usage
		}
	}
	if usage == nil {
		t.Fatal("usage event was not emitted")
	}
	if usage.InputTokens != 100 || usage.OutputTokens != 5 ||
		usage.TotalTokens != 105 || usage.CachedTokens != 80 {
		t.Fatalf("unexpected usage: %#v", usage)
	}
}
