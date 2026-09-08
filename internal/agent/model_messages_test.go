package agent

import (
	"strings"
	"testing"

	conversation "github.com/freesoulcode/foya/internal/conversation"
)

func TestTextMessageIncludesSelectedBrowserElementAsUntrustedContext(t *testing.T) {
	input := modelMessage(conversation.Message{
		Role:    conversation.RoleUser,
		Content: "Explain this button",
		BrowserElements: []conversation.BrowserElement{{
			PageURL:   "https://example.com/settings",
			PageTitle: "Settings",
			Tag:       "button",
			Selector:  "#save",
			Text:      "Save",
			HTML:      `<button id="save">Save</button>`,
		}},
	})

	if len(input.Parts) != 2 {
		t.Fatalf("parts = %d, want 2", len(input.Parts))
	}
	context := input.Parts[1].Text
	for _, want := range []string{
		"untrusted reference data",
		`"page_url":"https://example.com/settings"`,
		`"selector":"#save"`,
		`"text":"Save"`,
	} {
		if !strings.Contains(context, want) {
			t.Fatalf("browser context missing %q: %s", want, context)
		}
	}
}

func TestTextMessagesSkipEmptyAssistantMessages(t *testing.T) {
	messages := modelMessages([]conversation.Message{
		{Role: conversation.RoleUser, Content: "stop"},
		{Role: conversation.RoleAssistant, TurnStatus: "cancelled", TurnReason: "user_stop"},
		{Role: conversation.RoleUser, Content: "continue"},
	})

	if len(messages) != 2 {
		t.Fatalf("messages = %#v, want 2 provider-visible messages", messages)
	}
	if messages[0].Role != conversation.RoleUser || messages[0].Parts[0].Text != "stop" {
		t.Fatalf("first message = %#v", messages[0])
	}
	if messages[1].Role != conversation.RoleUser || messages[1].Parts[0].Text != "continue" {
		t.Fatalf("second message = %#v", messages[1])
	}
}

func TestTextMessagesPreserveCancelledReasoningAsContext(t *testing.T) {
	messages := modelMessages([]conversation.Message{{
		Role:       conversation.RoleAssistant,
		Reasoning:  "Found config in internal/config and queue handling in backend.",
		TurnStatus: "cancelled",
		TurnReason: "user_stop",
	}})

	if len(messages) != 1 {
		t.Fatalf("messages = %#v, want one provider-visible message", messages)
	}
	got := messages[0].Parts[0].Text
	if !strings.Contains(got, "Interrupted assistant analysis preserved before cancellation") ||
		!strings.Contains(got, "Found config") {
		t.Fatalf("cancelled reasoning was not preserved: %q", got)
	}
}
