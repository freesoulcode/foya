// Package title 根据会话首条用户消息生成标题。
//
// 标题生成是旁路任务:在首个 turn 开始时由后台 goroutine 触发,
// 不阻塞主回合。生成失败时由调用方走截断兜底。
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
	// DefaultTitle 是无任何可提炼文本时的兜底标题。
	DefaultTitle = "新对话"
	// MaxTitleChars 是标题硬上限(字符数,非字节)。
	MaxTitleChars = 50
	// MaxSourceBytes 是喂给模型的首条消息上限,超出截断。
	MaxSourceBytes = 8 * 1024
	// generateTimeout 是标题生成的整体超时。
	generateTimeout = 15 * time.Second
)

// systemPrompt 是标题生成的系统提示:硬约束单行输出,并对 CJK 等语言给出长度指引。
const systemPrompt = `You generate a short title for a conversation, based on the user's first message.

<rules>
- Use the same language as the user's message. For Chinese, Japanese, Korean and similar languages, produce an equivalently brief natural title (roughly 10–20 characters); for alphabetic languages, roughly 5–10 words.
- The title must summarize what the user wants to do, not repeat it verbatim.
- Hard limit: no more than 50 characters.
- Exactly one line. Never use a second line or more than one sentence.
- No quotes, no colons, no "Title:" / "标题：" prefix, no markdown, no list numbering.
- Do not include any thinking, explanation, or preamble.
- The entire text you return is used verbatim as the title.
</rules>`

const userPromptTpl = `Generate a title for this first user message:

%s`

var (
	thinkTagRe       = regexp.MustCompile(`(?is)<think>.*?(?:</think>|$)`)
	prefixRe         = regexp.MustCompile(`(?i)^\s*(?:title|标题)\s*[:：]\s*`)
	systemReminderRe = regexp.MustCompile(`(?is)<system-reminder>.*?</system-reminder>`)
	userMessageTagRe = regexp.MustCompile(`(?is)<user-message>(.*?)</user-message>`)
)

// Generate 调模型生成标题。任何失败(超时、provider 不支持、返回空)都返回空串,
// 由调用方走 Fallback。
func Generate(ctx context.Context, c provider.Completer, model, userText string) string {
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
		Model: model,
		Messages: []message.Message{
			{Role: message.RoleSystem, Content: systemPrompt},
			{Role: message.RoleUser, Content: fmt.Sprintf(userPromptTpl, source)},
		},
	})
	if err != nil {
		return ""
	}
	return cleanTitle(text)
}

// Fallback 在模型生成不可用时,用首条消息首行截断兜底。
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

// normalizeSource 清洗首条用户消息:剥标签、取首行、截断到上限。
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

// cleanTitle 清洗模型输出:剥 think 块、去前缀/引号、取首行、trim、截断。
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
