package compaction

import "strings"

// IsContextOverflow classifies errors that specifically indicate oversized model input.
// Output limits, quotas, and throttling are explicit vetoes because compaction cannot
// repair them.
func IsContextOverflow(text string) bool {
	value := strings.ToLower(text)
	if value == "" {
		return false
	}
	for _, veto := range []string{
		"rate limit",
		"rate_limit",
		"too many requests",
		"quota",
		"throttl",
		"output token",
		"completion token",
		"max_tokens",
		"maximum number of tokens to generate",
	} {
		if strings.Contains(value, veto) {
			return false
		}
	}
	for _, marker := range []string{
		"context length",
		"context_length",
		"context window",
		"context_window",
		"maximum context",
		"max context",
		"prompt is too long",
		"input is too long",
		"input too long",
		"input_too_large",
		"model_context_window_exceeded",
	} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	if strings.Contains(value, "request too large") ||
		strings.Contains(value, "request_too_large") {
		return containsAny(value, "prompt", "input token", "context")
	}
	if strings.Contains(value, "too many tokens") {
		return containsAny(value, "prompt", "input", "context")
	}
	return false
}

func containsAny(value string, markers ...string) bool {
	for _, marker := range markers {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}
