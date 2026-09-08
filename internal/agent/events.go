package agent

import (
	"context"
	"crypto/rand"
	"fmt"
	"time"

	conversation "github.com/freesoulcode/foya/internal/conversation"

	"github.com/freesoulcode/foya/internal/tool"
)

// emit appends an event and broadcasts it.
func (e *Engine) emit(ctx context.Context, sessionID string, kind conversation.Kind, payload any, mustDeliver bool) {
	ev := conversation.Event{
		Kind:    kind,
		Session: sessionID,
		RunID:   tool.RunIDFromContext(ctx),
		Time:    time.Now(),
		Payload: payload,
	}
	e.emitEvent(ctx, ev, mustDeliver)
}

func (e *Engine) emitEvent(ctx context.Context, ev conversation.Event, mustDeliver bool) {
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	seq, _ := e.log.Append(ctx, ev)
	ev.Seq = seq
	if mustDeliver {
		_ = e.bus.PublishMustDeliver(ctx, topic(ev.Session), ev)
	} else {
		e.bus.Publish(topic(ev.Session), ev)
	}
}

func topic(sessionID string) string { return "session:" + sessionID }

func newRunID() string {
	data := make([]byte, 12)
	if _, err := rand.Read(data); err != nil {
		return fmt.Sprintf("run-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", data)
}

func hasUserMessage(msgs []conversation.Message) bool {
	for _, m := range msgs {
		if m.Role == conversation.RoleUser {
			return true
		}
	}
	return false
}
