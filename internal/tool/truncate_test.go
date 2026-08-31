package tool

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateToolOutputReturnsUnchangedContentWithinLimits(t *testing.T) {
	content := "first\nsecond\n"
	result := truncateToolOutput(content, truncationOptions{
		MaxLines:  2,
		MaxBytes:  len(content),
		Direction: keepOutputHead,
	})

	if result.Truncated {
		t.Fatal("content within both limits was truncated")
	}
	if result.Content != content {
		t.Fatalf("content = %q, want %q", result.Content, content)
	}
}

func TestTruncateToolOutputKeepsHeadByLineLimit(t *testing.T) {
	result := truncateToolOutput("one\ntwo\nthree\nfour", truncationOptions{
		MaxLines:  2,
		MaxBytes:  1024,
		Direction: keepOutputHead,
	})

	if !result.Truncated {
		t.Fatal("expected truncation")
	}
	if !strings.HasPrefix(result.Content, "one\ntwo\n\n") {
		t.Fatalf("head was not retained: %q", result.Content)
	}
	if strings.Contains(result.Content, "three") || strings.Contains(result.Content, "four") {
		t.Fatalf("tail leaked into head preview: %q", result.Content)
	}
	if !strings.Contains(result.Content, "omitted 2 lines") {
		t.Fatalf("missing line omission marker: %q", result.Content)
	}
}

func TestTruncateToolOutputKeepsTailByLineLimit(t *testing.T) {
	result := truncateToolOutput("one\ntwo\nthree\nfour", truncationOptions{
		MaxLines:  2,
		MaxBytes:  1024,
		Direction: keepOutputTail,
	})

	if !result.Truncated {
		t.Fatal("expected truncation")
	}
	if !strings.HasSuffix(result.Content, "\n\nthree\nfour") {
		t.Fatalf("tail was not retained: %q", result.Content)
	}
	if strings.Contains(result.Content, "\none\n") || strings.Contains(result.Content, "\ntwo\n") {
		t.Fatalf("head leaked into tail preview: %q", result.Content)
	}
	if !strings.Contains(result.Content, "from the beginning") {
		t.Fatalf("missing tail direction marker: %q", result.Content)
	}
}

func TestTruncateToolOutputUsesUTF8SafeByteBoundaries(t *testing.T) {
	content := strings.Repeat("中", 20)
	for _, direction := range []truncationDirection{keepOutputHead, keepOutputTail} {
		result := truncateToolOutput(content, truncationOptions{
			MaxLines:  10,
			MaxBytes:  10,
			Direction: direction,
		})

		if !result.Truncated {
			t.Fatalf("direction %d: expected truncation", direction)
		}
		if !utf8.ValidString(result.Content) {
			t.Fatalf("direction %d: invalid UTF-8 output %q", direction, result.Content)
		}
		if result.KeptBytes > 10 {
			t.Fatalf("direction %d: kept %d bytes, limit is 10", direction, result.KeptBytes)
		}
		if !strings.Contains(result.Content, "omitted") {
			t.Fatalf("direction %d: missing omission marker", direction)
		}
	}
}

func TestTruncateToolOutputUsesByteLimitBeforeLineLimit(t *testing.T) {
	result := truncateToolOutput("12345\n67890\nabcde", truncationOptions{
		MaxLines:  10,
		MaxBytes:  11,
		Direction: keepOutputHead,
	})

	if !result.Truncated {
		t.Fatal("expected truncation")
	}
	if !strings.HasPrefix(result.Content, "12345\n67890\n\n") {
		t.Fatalf("unexpected byte-limited preview: %q", result.Content)
	}
	if strings.Contains(result.Content, "abcde") {
		t.Fatalf("content beyond byte limit was retained: %q", result.Content)
	}
}
