package videogen

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGenerateCreatesPollsAndDownloadsVideo(t *testing.T) {
	var polls atomic.Int32
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/contents/generations/tasks":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload["model"] != "doubao-seedance-2.0-mini" {
				t.Fatalf("unexpected request: %#v", payload)
			}
			if payload["ratio"] != "16:9" || payload["duration"] != float64(8) {
				t.Fatalf("video options = %#v", payload)
			}
			content, ok := payload["content"].([]any)
			if !ok || len(content) != 2 {
				t.Fatalf("content = %#v", payload["content"])
			}
			image, ok := content[1].(map[string]any)
			if !ok || image["role"] != "reference_image" {
				t.Fatalf("image content = %#v", content[1])
			}
			imageURL, _ := image["image_url"].(map[string]any)
			if imageURL["url"] != "data:image/heic;base64,aW1hZ2U=" {
				t.Fatalf("reference = %q", imageURL["url"])
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"task_id":"video-1","status":"queued"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/contents/generations/tasks/video-1":
			polls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"code":"success","data":{"task_id":"video-1","status":"SUCCESS","result_url":"`+serverURL+`/asset"}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/asset":
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = io.WriteString(w, "video-bytes")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	adapter := NewOpenAI(OpenAIConfig{BaseURL: server.URL + "/v1", APIKey: "secret", Protocol: ProtocolSeedance})
	adapter.pollInterval = time.Millisecond
	result, err := adapter.Generate(context.Background(), Request{
		Model: "doubao-seedance-2.0-mini", Prompt: "camera pans right", AspectRatio: "16:9",
		Duration: 8, Reference: []byte("image"), ReferenceMediaType: "image/heic",
	})
	if err != nil {
		t.Fatal(err)
	}
	if polls.Load() != 1 || result.MediaType != "video/mp4" || string(result.Data) != "video-bytes" {
		t.Fatalf("result = %#v, polls = %d", result, polls.Load())
	}
}

func TestGenerateReturnsProviderFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":"video-1","status":"processing"}`)
		default:
			_, _ = io.WriteString(w, `{"id":"video-1","status":"failed","error":{"message":"prompt rejected"}}`)
		}
	}))
	defer server.Close()

	adapter := NewOpenAI(OpenAIConfig{BaseURL: server.URL, Protocol: ProtocolSeedance})
	adapter.pollInterval = time.Millisecond
	_, err := adapter.Generate(context.Background(), Request{Model: "video-model", Prompt: "prompt"})
	if err == nil || !strings.Contains(err.Error(), "prompt rejected") {
		t.Fatalf("error = %v", err)
	}
}
