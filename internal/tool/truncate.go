package tool

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const defaultMaxOutputLines = 2000

type truncationDirection uint8

const (
	keepOutputHead truncationDirection = iota
	keepOutputTail
)

type truncationOptions struct {
	MaxLines  int
	MaxBytes  int
	Direction truncationDirection
}

type truncationResult struct {
	Content    string
	Truncated  bool
	TotalLines int
	TotalBytes int
	KeptLines  int
	KeptBytes  int
}

// truncateToolOutput applies independent line and UTF-8 byte limits. Shell
// output keeps the tail, while document-like tools keep the head.
func truncateToolOutput(content string, options truncationOptions) truncationResult {
	result := truncationResult{
		Content:    content,
		TotalBytes: len(content),
	}
	lines := splitOutputLines(content)
	result.TotalLines = len(lines)

	maxLines := options.MaxLines
	if maxLines <= 0 {
		maxLines = defaultMaxOutputLines
	}
	maxBytes := options.MaxBytes
	if maxBytes <= 0 {
		maxBytes = len(content)
	}
	if result.TotalLines <= maxLines && result.TotalBytes <= maxBytes {
		result.KeptLines = result.TotalLines
		result.KeptBytes = result.TotalBytes
		return result
	}

	var kept []string
	if options.Direction == keepOutputTail {
		kept = collectTail(lines, maxLines, maxBytes)
	} else {
		kept = collectHead(lines, maxLines, maxBytes)
	}
	preview := strings.Join(kept, "\n")
	if len(kept) == 0 && len(lines) > 0 && maxBytes > 0 {
		line := lines[0]
		if options.Direction == keepOutputTail {
			line = lines[len(lines)-1]
			preview = utf8Suffix(line, maxBytes)
		} else {
			preview = utf8Prefix(line, maxBytes)
		}
		if preview != "" {
			kept = []string{preview}
		}
	}

	result.Truncated = true
	result.KeptLines = len(kept)
	result.KeptBytes = len(preview)
	marker := truncationMarker(result, maxLines, maxBytes, options.Direction)
	if options.Direction == keepOutputTail {
		result.Content = marker
		if preview != "" {
			result.Content += "\n\n" + preview
		}
	} else {
		result.Content = preview
		if preview != "" {
			result.Content += "\n\n"
		}
		result.Content += marker
	}
	return result
}

func splitOutputLines(content string) []string {
	if content == "" {
		return nil
	}
	body := strings.TrimSuffix(content, "\n")
	return strings.Split(body, "\n")
}

func collectHead(lines []string, maxLines, maxBytes int) []string {
	kept := make([]string, 0, min(len(lines), maxLines))
	bytes := 0
	for i := 0; i < len(lines) && len(kept) < maxLines; i++ {
		size := len(lines[i])
		if len(kept) > 0 {
			size++
		}
		if bytes+size > maxBytes {
			break
		}
		kept = append(kept, lines[i])
		bytes += size
	}
	return kept
}

func collectTail(lines []string, maxLines, maxBytes int) []string {
	kept := make([]string, 0, min(len(lines), maxLines))
	bytes := 0
	for i := len(lines) - 1; i >= 0 && len(kept) < maxLines; i-- {
		size := len(lines[i])
		if len(kept) > 0 {
			size++
		}
		if bytes+size > maxBytes {
			break
		}
		kept = append(kept, lines[i])
		bytes += size
	}
	for left, right := 0, len(kept)-1; left < right; left, right = left+1, right-1 {
		kept[left], kept[right] = kept[right], kept[left]
	}
	return kept
}

func utf8Prefix(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	end := maxBytes
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end]
}

func utf8Suffix(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	start := len(value) - maxBytes
	for start < len(value) && !utf8.RuneStart(value[start]) {
		start++
	}
	return value[start:]
}

func truncationMarker(result truncationResult, maxLines, maxBytes int, direction truncationDirection) string {
	side := "end"
	if direction == keepOutputTail {
		side = "beginning"
	}
	omittedLines := result.TotalLines - result.KeptLines
	omittedBytes := result.TotalBytes - result.KeptBytes
	return fmt.Sprintf(
		"... (output truncated: omitted %d lines / %d bytes from the %s; limits: %d lines / %d bytes)",
		omittedLines,
		omittedBytes,
		side,
		maxLines,
		maxBytes,
	)
}
