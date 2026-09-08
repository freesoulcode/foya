package agent

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	model "github.com/freesoulcode/foya/internal/model"
)

const (
	// defaultTitle is used when no meaningful text can be extracted.
	defaultTitle = "New chat"
	// maxTitleChars is the hard title limit in characters.
	maxTitleChars = 50
	// maxTitleSourceBytes limits the first message sent to the title model.
	maxTitleSourceBytes = 8 * 1024
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

// generateTitleFromModel asks the model for a title and returns an empty string on failure.
func generateTitleFromModel(
	ctx context.Context,
	c model.Completer,
	modelID, reasoningEffort, userText string,
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

	text, err := c.Complete(ctx, model.Request{
		Model:           modelID,
		ReasoningEffort: reasoningEffort,
		Messages: []model.InputMessage{
			model.TextMessage(model.RoleSystem, systemPrompt),
			model.TextMessage(model.RoleUser, fmt.Sprintf(userPromptTpl, source)),
		},
	})
	if err != nil {
		return ""
	}
	return cleanTitle(text)
}

// fallbackTitle derives a title from the first line of the user's message.
func fallbackTitle(userText string) string {
	source := normalizeSource(userText)
	if source == "" {
		return defaultTitle
	}
	if t := truncateChars(source, maxTitleChars); t != "" {
		return t
	}
	return defaultTitle
}

// normalizeSource removes wrappers and bounds the first user message.
func normalizeSource(text string) string {
	if m := userMessageTagRe.FindStringSubmatch(text); len(m) > 1 {
		text = m[1]
	}
	text = systemReminderRe.ReplaceAllString(text, "")
	text = strings.TrimSpace(text)
	if b := []byte(text); len(b) > maxTitleSourceBytes {
		text = string(b[:maxTitleSourceBytes])
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
		ln = truncateChars(ln, maxTitleChars)
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
