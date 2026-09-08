package conversation

import (
	"encoding/json"

	model "github.com/freesoulcode/foya/internal/model"
)

func decodePayload(kind Kind, raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var target any
	switch kind {
	case KindMessageEnd, KindMessageImported:
		target = &Message{}
	case KindCompactionCompleted:
		target = &Checkpoint{}
	case KindContextRequestAccepted:
		target = &AcceptedBoundary{}
	case KindHistoryRewound:
		target = &HistoryRewound{}
	case KindFileReviewResolved:
		target = &FileReviewResolved{}
	case KindSessionForked:
		target = &SessionForked{}
	case KindUsageUpdated:
		target = &model.Usage{}
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
	case *Message:
		return *value
	case *Checkpoint:
		return *value
	case *AcceptedBoundary:
		return *value
	case *HistoryRewound:
		return *value
	case *FileReviewResolved:
		return *value
	case *SessionForked:
		return *value
	case *model.Usage:
		return *value
	default:
		return target
	}
}

func activeMessageEvents(events []Event) []Event {
	active := make([]Event, 0, len(events))
	for _, ev := range events {
		switch ev.Kind {
		case KindMessageEnd, KindMessageImported:
			active = append(active, ev)
		case KindHistoryRewound:
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

func messageFromEvent(ev Event) (Message, bool) {
	switch value := ev.Payload.(type) {
	case Message:
		return value, true
	case *Message:
		if value != nil {
			return *value, true
		}
	}
	return Message{}, false
}

func historyRewindFromPayload(payload any) (HistoryRewound, bool) {
	switch value := payload.(type) {
	case HistoryRewound:
		return value, true
	case *HistoryRewound:
		if value != nil {
			return *value, true
		}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return HistoryRewound{}, false
	}
	var rewind HistoryRewound
	if err := json.Unmarshal(data, &rewind); err != nil || rewind.TargetUserSeq == 0 {
		return HistoryRewound{}, false
	}
	return rewind, true
}

func fileReviewResolvedFromPayload(payload any) (FileReviewResolved, bool) {
	switch value := payload.(type) {
	case FileReviewResolved:
		return value, true
	case *FileReviewResolved:
		if value != nil {
			return *value, true
		}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return FileReviewResolved{}, false
	}
	var resolved FileReviewResolved
	if err := json.Unmarshal(data, &resolved); err != nil ||
		resolved.ThroughSeq == 0 ||
		resolved.Action == "" {
		return FileReviewResolved{}, false
	}
	return resolved, true
}
