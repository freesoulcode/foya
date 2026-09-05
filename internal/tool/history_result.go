package tool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/message"
)

const (
	HistoryReadToolResultName = "history_read_tool_result"
	defaultHistoryReadRunes   = 4_000
	maxHistoryReadRunes       = 12_000
	defaultHistoryMatches     = 20
	maxHistoryMatches         = 100
	maxHistoryMatchRunes      = 500
)

type activeMessageEventReader interface {
	ActiveMessageEvent(
		ctx context.Context,
		session string,
		seq event.Seq,
	) (event.Event, bool, error)
}

type historyReadToolResult struct {
	events activeMessageEventReader
}

type historyReadToolResultParams struct {
	EventSeq       uint64 `json:"event_seq"`
	ToolCallID     string `json:"tool_call_id"`
	ExpectedSHA256 string `json:"expected_sha256,omitempty"`
	Operation      string `json:"operation,omitempty"`
	Offset         int    `json:"offset,omitempty"`
	Limit          int    `json:"limit,omitempty"`
	Query          string `json:"query,omitempty"`
	MaxMatches     int    `json:"max_matches,omitempty"`
}

type historyReadToolResultResponse struct {
	EventSeq   uint64               `json:"event_seq"`
	ToolCallID string               `json:"tool_call_id"`
	SHA256     string               `json:"sha256"`
	Operation  string               `json:"operation"`
	Offset     int                  `json:"offset,omitempty"`
	NextOffset int                  `json:"next_offset,omitempty"`
	TotalRunes int                  `json:"total_runes"`
	Content    string               `json:"content,omitempty"`
	Matches    []historyResultMatch `json:"matches,omitempty"`
}

type historyResultMatch struct {
	Line int    `json:"line"`
	Text string `json:"text"`
}

func NewHistoryReadToolResult(events activeMessageEventReader) Tool {
	return &historyReadToolResult{events: events}
}

func (t *historyReadToolResult) Name() string { return HistoryReadToolResultName }
func (t *historyReadToolResult) Exposure() Exposure {
	return ExposureDeferred
}
func (t *historyReadToolResult) Description() string {
	return "Inspect, page through, or search a large canonical tool result referenced by compacted context."
}
func (t *historyReadToolResult) Spec() []byte {
	return []byte(`{
		"type":"object",
		"properties":{
			"event_seq":{"type":"integer","minimum":1,"description":"Event sequence from the tool result reference."},
			"tool_call_id":{"type":"string","minLength":1,"description":"Tool call ID from the reference."},
			"expected_sha256":{"type":"string","description":"Optional SHA-256 from the reference. The read fails if it no longer matches."},
			"operation":{"type":"string","enum":["inspect","read","search"],"description":"Operation to perform. Defaults to read."},
			"offset":{"type":"integer","minimum":0,"description":"Rune offset to start reading from. Defaults to 0."},
			"limit":{"type":"integer","minimum":1,"maximum":12000,"description":"Maximum runes to return for read. Defaults to 4000."},
			"query":{"type":"string","description":"Literal case-insensitive query required by search."},
			"max_matches":{"type":"integer","minimum":1,"maximum":100,"description":"Maximum matching lines for search. Defaults to 20."}
		},
		"required":["event_seq","tool_call_id"],
		"additionalProperties":false
	}`)
}

func (t *historyReadToolResult) Run(ctx context.Context, call Call) (Result, error) {
	if t.events == nil {
		return errResult("history event storage is unavailable"), nil
	}
	sessionID := SessionIDFromContext(ctx)
	if strings.TrimSpace(sessionID) == "" {
		return errResult("session id is required"), nil
	}
	var params historyReadToolResultParams
	if err := json.Unmarshal(call.Input, &params); err != nil {
		return errResult("invalid arguments: " + err.Error()), nil
	}
	params.ToolCallID = strings.TrimSpace(params.ToolCallID)
	if params.EventSeq == 0 || params.ToolCallID == "" {
		return errResult("event_seq and tool_call_id are required"), nil
	}
	params.Operation = strings.ToLower(strings.TrimSpace(params.Operation))
	if params.Operation == "" {
		params.Operation = "read"
	}
	if params.Operation != "inspect" &&
		params.Operation != "read" &&
		params.Operation != "search" {
		return errResult("operation must be inspect, read, or search"), nil
	}
	if params.Offset < 0 {
		return errResult("offset must be non-negative"), nil
	}
	if params.Limit <= 0 {
		params.Limit = defaultHistoryReadRunes
	}
	if params.Limit > maxHistoryReadRunes {
		return errResult(fmt.Sprintf("limit must not exceed %d", maxHistoryReadRunes)), nil
	}

	ev, ok, err := t.events.ActiveMessageEvent(
		ctx,
		sessionID,
		event.Seq(params.EventSeq),
	)
	if err != nil {
		return errResult("read tool result failed: " + err.Error()), nil
	}
	if !ok {
		return errResult("active tool result was not found"), nil
	}
	item, ok := ev.Payload.(message.Message)
	if !ok || item.Role != message.RoleTool || item.ToolCallID != params.ToolCallID {
		return errResult("event does not match the referenced tool result"), nil
	}

	runes := []rune(item.Content)
	sum := sha256.Sum256([]byte(item.Content))
	digest := hex.EncodeToString(sum[:])
	if expected := strings.ToLower(strings.TrimSpace(params.ExpectedSHA256)); expected != "" &&
		expected != digest {
		return errResult("tool result digest no longer matches the reference"), nil
	}
	response := historyReadToolResultResponse{
		EventSeq:   params.EventSeq,
		ToolCallID: item.ToolCallID,
		SHA256:     digest,
		Operation:  params.Operation,
		TotalRunes: len(runes),
	}
	switch params.Operation {
	case "inspect":
		// Metadata already populated.
	case "search":
		query := strings.TrimSpace(params.Query)
		if query == "" {
			return errResult("query is required for search"), nil
		}
		if params.MaxMatches <= 0 {
			params.MaxMatches = defaultHistoryMatches
		}
		if params.MaxMatches > maxHistoryMatches {
			return errResult(fmt.Sprintf(
				"max_matches must not exceed %d",
				maxHistoryMatches,
			)), nil
		}
		response.Matches = literalLineMatches(
			item.Content,
			query,
			params.MaxMatches,
		)
	default:
		if params.Offset > len(runes) {
			return errResult(fmt.Sprintf(
				"offset %d exceeds tool result length %d",
				params.Offset,
				len(runes),
			)), nil
		}
		end := len(runes)
		if params.Limit < len(runes)-params.Offset {
			end = params.Offset + params.Limit
		}
		if end < len(runes) {
			response.NextOffset = end
		}
		response.Offset = params.Offset
		response.Content = string(runes[params.Offset:end])
	}
	data, _ := json.Marshal(response)
	return textResult(string(data)), nil
}

func literalLineMatches(content, query string, limit int) []historyResultMatch {
	lowerQuery := strings.ToLower(query)
	matches := make([]historyResultMatch, 0, min(limit, defaultHistoryMatches))
	for index, line := range strings.Split(content, "\n") {
		if !strings.Contains(strings.ToLower(line), lowerQuery) {
			continue
		}
		runes := []rune(line)
		if len(runes) > maxHistoryMatchRunes {
			line = string(runes[:maxHistoryMatchRunes]) + "..."
		}
		matches = append(matches, historyResultMatch{
			Line: index + 1,
			Text: line,
		})
		if len(matches) >= limit {
			break
		}
	}
	return matches
}
