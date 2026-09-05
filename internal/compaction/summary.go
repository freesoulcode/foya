package compaction

import (
	"fmt"
	"strings"
)

var requiredSummarySections = []string{
	"## Goal",
	"## Progress",
	"## Key Decisions",
	"## Next Steps",
	"## Critical Context",
}

// OutputTokenLimit keeps compaction generation independent from normal replies.
func OutputTokenLimit(configured int64) int64 {
	if configured <= 0 {
		return DefaultOutputTokens
	}
	return min(configured, MaxOutputTokens)
}

// ValidateSummary enforces the continuation checkpoint wire format.
func ValidateSummary(summary string) error {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return fmt.Errorf("checkpoint is empty")
	}

	contents := make([][]string, len(requiredSummarySections))
	activeSection := -1
	expectedSection := 0
	inFence := false
	fenceMarker := ""

	for _, line := range strings.Split(summary, "\n") {
		trimmed := strings.TrimSpace(line)
		if marker := markdownFenceMarker(trimmed); marker != "" {
			if !inFence {
				inFence = true
				fenceMarker = marker
			} else if marker == fenceMarker {
				inFence = false
				fenceMarker = ""
			}
			if activeSection >= 0 {
				contents[activeSection] = append(contents[activeSection], line)
			}
			continue
		}
		if !inFence && strings.HasPrefix(trimmed, "## ") {
			index := summarySectionIndex(trimmed)
			if index < 0 {
				return fmt.Errorf("unexpected checkpoint section %q", trimmed)
			}
			if index != expectedSection {
				return fmt.Errorf(
					"checkpoint section %q is out of order",
					trimmed,
				)
			}
			activeSection = index
			expectedSection++
			continue
		}
		if activeSection < 0 {
			if trimmed != "" {
				return fmt.Errorf("checkpoint has content before its first section")
			}
			continue
		}
		contents[activeSection] = append(contents[activeSection], line)
	}

	if inFence {
		return fmt.Errorf("checkpoint contains an unclosed code fence")
	}
	if expectedSection != len(requiredSummarySections) {
		return fmt.Errorf(
			"checkpoint is missing section %q",
			requiredSummarySections[expectedSection],
		)
	}
	for i, lines := range contents {
		if !hasSubstantiveContent(lines) {
			return fmt.Errorf(
				"checkpoint section %q is empty",
				requiredSummarySections[i],
			)
		}
	}
	if hasTruncationMarker(summary) {
		return fmt.Errorf("checkpoint appears truncated")
	}
	return nil
}

func summarySectionIndex(heading string) int {
	for i, section := range requiredSummarySections {
		if heading == section {
			return i
		}
	}
	return -1
}

func markdownFenceMarker(line string) string {
	switch {
	case strings.HasPrefix(line, "```"):
		return "```"
	case strings.HasPrefix(line, "~~~"):
		return "~~~"
	default:
		return ""
	}
}

func hasSubstantiveContent(lines []string) bool {
	for _, line := range lines {
		value := strings.TrimSpace(line)
		if value == "" || strings.HasPrefix(value, "###") {
			continue
		}
		value = strings.TrimLeft(value, "-*+0123456789.()[] \t")
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func hasTruncationMarker(summary string) bool {
	tail := strings.ToLower(strings.TrimSpace(summary))
	for _, marker := range []string{
		"[truncated]",
		"<truncated>",
		"to be continued",
		"[continued]",
	} {
		if strings.HasSuffix(tail, marker) {
			return true
		}
	}
	return false
}
