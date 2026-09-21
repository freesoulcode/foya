package feishu

import (
	"strings"
	"testing"
)

func TestLocalizedMessage(t *testing.T) {
	english := localizedMessage("en-US", "task_stopped")
	if english != "Stopped the current task." {
		t.Fatalf("English message = %q", english)
	}
	translated := localizedMessage("zh-CN", "task_stopped")
	if translated == "" || translated == english {
		t.Fatalf("translated message = %q", translated)
	}
	formatted := localizedMessage(
		"zh-CN",
		"unsupported_attachment",
		"file",
	)
	if !strings.Contains(formatted, "file") {
		t.Fatalf("formatted message = %q", formatted)
	}
	if got := localizedMessage(
		"unknown",
		"new_chat",
	); got != "Started a new chat." {
		t.Fatalf("fallback message = %q", got)
	}
}
