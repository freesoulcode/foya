package openai

import (
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
			ReasoningEffort string `json:"reasoning_effort"`
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
		Messages:        []message.Message{{Role: message.RoleUser, Content: "hello"}},
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
