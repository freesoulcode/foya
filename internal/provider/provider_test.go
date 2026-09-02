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
