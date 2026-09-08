package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	conversation "github.com/freesoulcode/foya/internal/conversation"
)

type historyResultReader struct {
	session string
	event   conversation.Event
}

func (r historyResultReader) ActiveMessageEvent(
	_ context.Context,
	session string,
	seq conversation.Seq,
) (conversation.Event, bool, error) {
	if session != r.session || seq != r.event.Seq {
		return conversation.Event{}, false, nil
	}
	return r.event, true, nil
}

func TestHistoryReadToolResultPagesCanonicalOutput(t *testing.T) {
	content := "0123456789"
	reader := historyResultReader{
		session: "session-1",
		event: conversation.Event{
			Seq:     42,
			Kind:    conversation.KindMessageEnd,
			Session: "session-1",
			Payload: conversation.Message{
				Role:       conversation.RoleTool,
				ToolCallID: "call-1",
				Content:    content,
			},
		},
	}
	historyTool := NewHistoryReadToolResult(reader)
	result, err := historyTool.Run(
		WithSessionID(context.Background(), "session-1"),
		Call{Input: []byte(`{
			"event_seq":42,
			"tool_call_id":"call-1",
			"offset":3,
			"limit":4
		}`)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || len(result.Content) != 1 {
		t.Fatalf("result = %#v", result)
	}
	var response historyReadToolResultResponse
	if err := json.Unmarshal([]byte(result.Content[0].Text), &response); err != nil {
		t.Fatal(err)
	}
	if response.Content != "3456" || response.NextOffset != 7 ||
		response.TotalRunes != len([]rune(content)) {
		t.Fatalf("response = %#v", response)
	}
}

func TestHistoryReadToolResultEnforcesSessionAndCallID(t *testing.T) {
	reader := historyResultReader{
		session: "session-1",
		event: conversation.Event{
			Seq:     42,
			Kind:    conversation.KindMessageEnd,
			Session: "session-1",
			Payload: conversation.Message{
				Role:       conversation.RoleTool,
				ToolCallID: "call-1",
				Content:    "secret",
			},
		},
	}
	historyTool := NewHistoryReadToolResult(reader)
	for name, test := range map[string]struct {
		ctx   context.Context
		input string
	}{
		"wrong session": {
			ctx:   WithSessionID(context.Background(), "session-2"),
			input: `{"event_seq":42,"tool_call_id":"call-1"}`,
		},
		"wrong call": {
			ctx:   WithSessionID(context.Background(), "session-1"),
			input: `{"event_seq":42,"tool_call_id":"call-2"}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			result, err := historyTool.Run(
				test.ctx,
				Call{Input: []byte(test.input)},
			)
			if err != nil {
				t.Fatal(err)
			}
			if !result.IsError || len(result.Content) == 0 ||
				strings.Contains(result.Content[0].Text, "secret") {
				t.Fatalf("result = %#v", result)
			}
		})
	}
}

func TestHistoryReadToolResultInspectSearchAndDigest(t *testing.T) {
	content := "alpha\nBeta needle\nsecond needle line\nomega"
	reader := historyResultReader{
		session: "session-1",
		event: conversation.Event{
			Seq:     42,
			Kind:    conversation.KindMessageEnd,
			Session: "session-1",
			Payload: conversation.Message{
				Role:       conversation.RoleTool,
				ToolCallID: "call-1",
				Content:    content,
			},
		},
	}
	historyTool := NewHistoryReadToolResult(reader)
	ctx := WithSessionID(context.Background(), "session-1")

	inspect, err := historyTool.Run(ctx, Call{Input: []byte(
		`{"event_seq":42,"tool_call_id":"call-1","operation":"inspect"}`,
	)})
	if err != nil || inspect.IsError {
		t.Fatalf("inspect = %#v err=%v", inspect, err)
	}
	var metadata historyReadToolResultResponse
	if err := json.Unmarshal([]byte(inspect.Content[0].Text), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Content != "" || metadata.SHA256 == "" ||
		metadata.TotalRunes != len([]rune(content)) {
		t.Fatalf("metadata = %#v", metadata)
	}

	search, err := historyTool.Run(ctx, Call{Input: []byte(
		`{"event_seq":42,"tool_call_id":"call-1","operation":"search","query":"NEEDLE"}`,
	)})
	if err != nil || search.IsError {
		t.Fatalf("search = %#v err=%v", search, err)
	}
	var matches historyReadToolResultResponse
	if err := json.Unmarshal([]byte(search.Content[0].Text), &matches); err != nil {
		t.Fatal(err)
	}
	if len(matches.Matches) != 2 ||
		matches.Matches[0].Line != 2 ||
		matches.Matches[1].Line != 3 {
		t.Fatalf("matches = %#v", matches.Matches)
	}

	mismatch, err := historyTool.Run(ctx, Call{Input: []byte(
		`{"event_seq":42,"tool_call_id":"call-1","expected_sha256":"wrong"}`,
	)})
	if err != nil || !mismatch.IsError {
		t.Fatalf("digest mismatch = %#v err=%v", mismatch, err)
	}
}
