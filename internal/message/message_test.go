package message

import "testing"

func TestModelContentUsesHiddenUserOverride(t *testing.T) {
	item := Message{
		Role:                 RoleUser,
		Content:              "visible request",
		ModelContentOverride: "loaded skill instructions\nvisible request",
	}
	if got := item.ModelContent(); got != item.ModelContentOverride {
		t.Fatalf("ModelContent() = %q, want hidden override", got)
	}
	if item.Content != "visible request" {
		t.Fatalf("visible content changed: %q", item.Content)
	}
}
