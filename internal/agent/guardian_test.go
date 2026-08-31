package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/freesoulcode/foya/internal/approval"
	"github.com/freesoulcode/foya/internal/provider"
)

type guardianCompleter struct {
	response string
	err      error
	request  provider.Request
}

func (c *guardianCompleter) Complete(_ context.Context, request provider.Request) (string, error) {
	c.request = request
	return c.response, c.err
}

func TestGuardianReviewerUsesSessionModelAndParsesDecision(t *testing.T) {
	completer := &guardianCompleter{
		response: `{"decision":"approve","reason":"scoped build command"}`,
	}
	reviewer := guardianReviewer{
		completer:       completer,
		model:           "model-1",
		reasoningEffort: "high",
		userRequest:     "run the tests",
		projectPath:     "/workspace",
	}
	review, err := reviewer.Review(context.Background(), approval.Request{
		ToolName: "bash",
		Action:   "execute",
		Detail:   "go test ./...",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !review.Approved || review.Reason != "scoped build command" {
		t.Fatalf("review = %#v", review)
	}
	if completer.request.Model != "model-1" || completer.request.ReasoningEffort != "high" {
		t.Fatalf("request = %#v", completer.request)
	}
	if len(completer.request.Tools) != 0 {
		t.Fatalf("guardian must not receive tools: %#v", completer.request.Tools)
	}
}

func TestGuardianReviewerFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name     string
		response string
		err      error
	}{
		{name: "provider error", err: errors.New("offline")},
		{name: "markdown", response: "```json\n{\"decision\":\"approve\",\"reason\":\"ok\"}\n```"},
		{name: "unknown decision", response: `{"decision":"maybe","reason":"unclear"}`},
		{name: "missing reason", response: `{"decision":"approve","reason":""}`},
		{name: "extra field", response: `{"decision":"approve","reason":"ok","extra":true}`},
		{name: "trailing json", response: `{"decision":"approve","reason":"ok"} {}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			reviewer := guardianReviewer{
				completer: &guardianCompleter{response: test.response, err: test.err},
			}
			if review, err := reviewer.Review(context.Background(), approval.Request{}); err == nil || review.Approved {
				t.Fatalf("review = %#v, err = %v", review, err)
			}
		})
	}
}

func TestGuardianReviewerRequiresCompleter(t *testing.T) {
	_, err := (guardianReviewer{}).Review(context.Background(), approval.Request{})
	if !errors.Is(err, approval.ErrGuardianUnavailable) {
		t.Fatalf("err = %v", err)
	}
}
