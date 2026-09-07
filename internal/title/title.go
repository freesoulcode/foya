// Package title generates chat titles from the first user message.
//
// Generation runs in a background goroutine and never blocks the main turn.
package title

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/provider"
)

const (
	// DefaultTitle is used when no meaningful text can be extracted.
	DefaultTitle = "New chat"
	// MaxTitleChars is the hard title limit in characters.
	MaxTitleChars = 50
	// MaxSourceBytes limits the first message sent to the title model.
	MaxSourceBytes = 8 * 1024
	// generateTimeout bounds the complete title request.
	generateTimeout = 15 * time.Second
)

// systemPrompt requires one concise title in the user's language.
const systemPrompt = `You generate a short title for a conversation, based on the user's first message.

<rules>
- Use the same language as the user's message. For Chinese, Japanese, Korean and similar languages, produce an equivalently brief natural title (roughly 10–20 characters); for alphabetic languages, roughly 5–10 words.
- The title must summarize what the user wants to do, not repeat it verbatim.
- Hard limit: no more than 50 characters.
- Exactly one line. Never use a second line or more than one sentence.
- No quotes, no colons, no localized "Title:" prefix, no markdown, no list numbering.
- Do not include any thinking, explanation, or preamble.
- The entire text you return is used verbatim as the title.
</rules>`

const userPromptTpl = `Generate a title for this first user message:

%s`

var (
	thinkTagRe       = regexp.MustCompile(`(?is)<think>.*?(?:</think>|$)`)
	prefixRe         = regexp.MustCompile(`(?i)^\s*(?:title|\x{6807}\x{9898})\s*[:\x{ff1a}]\s*`)
	systemReminderRe = regexp.MustCompile(`(?is)<system-reminder>.*?</system-reminder>`)
	userMessageTagRe = regexp.MustCompile(`(?is)<user-message>(.*?)</user-message>`)
)

// Generate asks the model for a title and returns an empty string on failure.
func Generate(
	ctx context.Context,
	c provider.Completer,
	model, reasoningEffort, userText string,
) string {
	if c == nil || strings.TrimSpace(userText) == "" {
		return ""
	}
	source := normalizeSource(userText)
	if source == "" {
		return ""
	}

	ctx, cancel := context.WithTimeout(ctx, generateTimeout)
	defer cancel()

	text, err := c.Complete(ctx, provider.Request{
		Model:           model,
		ReasoningEffort: reasoningEffort,
		Messages: []provider.InputMessage{
			provider.TextMessage(message.Message{Role: message.RoleSystem, Content: systemPrompt}),
			provider.TextMessage(message.Message{Role: message.RoleUser, Content: fmt.Sprintf(userPromptTpl, source)}),
		},
	})
	if err != nil {
		return ""
	}
	return cleanTitle(text)
}

// Fallback derives a title from the first line of the user's message.
func Fallback(userText string) string {
	source := normalizeSource(userText)
	if source == "" {
		return DefaultTitle
	}
	if t := truncateChars(source, MaxTitleChars); t != "" {
		return t
	}
	return DefaultTitle
}

// normalizeSource removes wrappers and bounds the first user message.
func normalizeSource(text string) string {
	if m := userMessageTagRe.FindStringSubmatch(text); len(m) > 1 {
		text = m[1]
	}
	text = systemReminderRe.ReplaceAllString(text, "")
	text = strings.TrimSpace(text)
	if b := []byte(text); len(b) > MaxSourceBytes {
		text = string(b[:MaxSourceBytes])
	}
	return text
}

// cleanTitle removes reasoning, prefixes, quotes, extra lines, and overflow.
func cleanTitle(text string) string {
	text = thinkTagRe.ReplaceAllString(text, "")
	text = prefixRe.ReplaceAllString(text, "")
	lines := strings.Split(text, "\n")
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		ln = trimQuotes(ln)
		ln = truncateChars(ln, MaxTitleChars)
		if ln != "" {
			return ln
		}
	}
	return ""
}

func trimQuotes(s string) string {
	pairs := [][2]rune{
		{'"', '"'}, {'\'', '\''}, {'`', '`'},
		{'“', '”'}, {'「', '」'}, {'『', '』'},
	}
	rs := []rune(s)
	if len(rs) < 2 {
		return s
	}
	for _, p := range pairs {
		if rs[0] == p[0] && rs[len(rs)-1] == p[1] {
			return string(rs[1 : len(rs)-1])
		}
	}
	return s
}

func truncateChars(s string, max int) string {
	rs := []rune(s)
	if len(rs) <= max {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(string(rs[:max]))
}
