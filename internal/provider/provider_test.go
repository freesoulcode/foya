package provider

import (
	"strings"
	"testing"

	"github.com/freesoulcode/foya/internal/message"
)

func TestTextMessageIncludesSelectedBrowserElementAsUntrustedContext(t *testing.T) {
	input := TextMessage(message.Message{
		Role:    message.RoleUser,
		Content: "Explain this button",
		BrowserElements: []message.BrowserElement{{
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
	messages := TextMessages([]message.Message{
		{Role: message.RoleUser, Content: "stop"},
		{Role: message.RoleAssistant, TurnStatus: "cancelled", TurnReason: "user_stop"},
		{Role: message.RoleUser, Content: "continue"},
	})

	if len(messages) != 2 {
		t.Fatalf("messages = %#v, want 2 provider-visible messages", messages)
	}
	if messages[0].Role != message.RoleUser || messages[0].Parts[0].Text != "stop" {
		t.Fatalf("first message = %#v", messages[0])
	}
	if messages[1].Role != message.RoleUser || messages[1].Parts[0].Text != "continue" {
		t.Fatalf("second message = %#v", messages[1])
	}
}

func TestTextMessagesPreserveCancelledReasoningAsContext(t *testing.T) {
	messages := TextMessages([]message.Message{{
		Role:       message.RoleAssistant,
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
