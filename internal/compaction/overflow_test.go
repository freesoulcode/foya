package compaction

import "testing"

func TestIsContextOverflow(t *testing.T) {
	for _, text := range []string{
		"maximum context length exceeded",
		"prompt is too long",
		"model_context_window_exceeded",
		"request_too_large: prompt exceeds context window",
		"too many tokens in input prompt",
	} {
		if !IsContextOverflow(text) {
			t.Fatalf("expected context overflow for %q", text)
		}
	}

	for _, text := range []string{
		"rate limit exceeded for input tokens",
		"output token limit exceeded",
		"too many requests",
		"request_too_large",
		"quota exceeded",
	} {
		if IsContextOverflow(text) {
			t.Fatalf("unexpected context overflow for %q", text)
		}
	}
}
