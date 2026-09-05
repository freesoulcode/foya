package state

import (
	"encoding/json"

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
	case event.KindMessageEnd, event.KindMessageImported:
		target = &message.Message{}
	case event.KindCompactionCompleted:
		target = &compaction.Checkpoint{}
	case event.KindContextRequestAccepted:
		target = &compaction.AcceptedBoundary{}
	case event.KindHistoryRewound:
		target = &event.HistoryRewound{}
	case event.KindFileReviewResolved:
		target = &event.FileReviewResolved{}
	case event.KindSessionForked:
		target = &event.SessionForked{}
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
	case *compaction.AcceptedBoundary:
		return *value
	case *event.HistoryRewound:
		return *value
	case *event.FileReviewResolved:
		return *value
	case *event.SessionForked:
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
		case event.KindMessageEnd, event.KindMessageImported:
			active = append(active, ev)
		case event.KindHistoryRewound:
			rewind, ok := historyRewindFromPayload(ev.Payload)
			if !ok {
				continue
			}
			for i := len(active) - 1; i >= 0; i-- {
				if active[i].Seq == rewind.TargetUserSeq {
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

func historyRewindFromPayload(payload any) (event.HistoryRewound, bool) {
	switch value := payload.(type) {
	case event.HistoryRewound:
		return value, true
	case *event.HistoryRewound:
		if value != nil {
			return *value, true
		}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return event.HistoryRewound{}, false
	}
	var rewind event.HistoryRewound
	if err := json.Unmarshal(data, &rewind); err != nil || rewind.TargetUserSeq == 0 {
		return event.HistoryRewound{}, false
	}
	return rewind, true
}

func fileReviewResolvedFromPayload(payload any) (event.FileReviewResolved, bool) {
	switch value := payload.(type) {
	case event.FileReviewResolved:
		return value, true
	case *event.FileReviewResolved:
		if value != nil {
			return *value, true
		}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return event.FileReviewResolved{}, false
	}
	var resolved event.FileReviewResolved
	if err := json.Unmarshal(data, &resolved); err != nil ||
		resolved.ThroughSeq == 0 ||
		resolved.Action == "" {
		return event.FileReviewResolved{}, false
	}
	return resolved, true
}
