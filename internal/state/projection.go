package state

import (
	"encoding/json"
	"strings"

	"github.com/freesoulcode/foya/internal/compaction"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/provider"
)

func decodePayload(kind event.Kind, raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var target any
	switch kind {
	case event.KindMessageEnd:
		target = &message.Message{}
	case event.KindCompactionCompleted:
		target = &compaction.Checkpoint{}
	case event.KindHistoryBranched:
		target = &event.HistoryBranched{}
	case event.KindUsageUpdated:
		target = &provider.Usage{}
	default:
		var value any
		if json.Unmarshal(raw, &value) == nil {
			return value
		}
		return nil
	}
	if json.Unmarshal(raw, target) != nil {
		return nil
	}
	switch value := target.(type) {
	case *message.Message:
		return *value
	case *compaction.Checkpoint:
		return *value
	case *event.HistoryBranched:
		return *value
	case *provider.Usage:
		return *value
	default:
		return target
	}
}

func activeMessageEvents(events []event.Event) []event.Event {
	active := make([]event.Event, 0, len(events))
	for _, ev := range events {
		switch ev.Kind {
		case event.KindMessageEnd:
			active = append(active, ev)
		case event.KindHistoryBranched:
			branch, ok := historyBranchFromPayload(ev.Payload)
			if !ok {
				continue
			}
			for i := len(active) - 1; i >= 0; i-- {
				if active[i].Seq == branch.TargetUserSeq {
					active = active[:i]
					break
				}
			}
		}
	}
	return active
}

func messageFromEvent(ev event.Event) (message.Message, bool) {
	switch value := ev.Payload.(type) {
	case message.Message:
		return value, true
	case *message.Message:
		if value != nil {
			return *value, true
		}
	}
	return message.Message{}, false
}

func historyBranchFromPayload(payload any) (event.HistoryBranched, bool) {
	switch value := payload.(type) {
	case event.HistoryBranched:
		return value, true
	case *event.HistoryBranched:
		if value != nil {
			return *value, true
		}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return event.HistoryBranched{}, false
	}
	var branch event.HistoryBranched
	if err := json.Unmarshal(data, &branch); err != nil || branch.TargetUserSeq == 0 {
		return event.HistoryBranched{}, false
	}
	return branch, true
}

func branchEffects(events []event.Event) []event.BranchEffect {
	toolResults := make(map[string]message.Message)
	for _, ev := range events {
		msg, ok := messageFromEvent(ev)
		if ok && msg.Role == message.RoleTool {
			toolResults[msg.ToolCallID] = msg
		}
	}

	effects := make([]event.BranchEffect, 0)
	for _, ev := range events {
		msg, ok := messageFromEvent(ev)
		if !ok || msg.Role != message.RoleAssistant {
			continue
		}
		for _, call := range msg.ToolCalls {
			switch call.Name {
			case "bash":
				effects = append(effects, event.BranchEffect{
					Tool:   call.Name,
					Detail: toolArgument(call.Input, "command"),
				})
			case "write", "edit":
				result, completed := toolResults[call.ID]
				if !completed || result.Diff == "" {
					continue
				}
				effects = append(effects, event.BranchEffect{
					Tool:   call.Name,
					Detail: toolArgument(call.Input, "path"),
				})
			}
			if len(effects) == 20 {
				return effects
			}
		}
	}
	return effects
}

func toolArgument(input json.RawMessage, key string) string {
	var args map[string]any
	if err := json.Unmarshal(input, &args); err != nil {
		return ""
	}
	value, _ := args[key].(string)
	const maxRunes = 160
	runes := []rune(strings.TrimSpace(value))
	if len(runes) > maxRunes {
		return string(runes[:maxRunes]) + "..."
	}
	return string(runes)
}
