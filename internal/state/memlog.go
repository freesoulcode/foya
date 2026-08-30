// 内存版事件日志与历史投影(脚手架阶段;后续由 JSONL + SQLite 替换)。
package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/freesoulcode/foya/internal/compaction"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/message"
)

var (
	ErrActiveUserMessageNotFound = errors.New("active user message not found")
	ErrMessageUnchanged          = errors.New("edited message is unchanged")
	ErrBranchChanged             = errors.New("active history changed after branch preview")
)

// BranchResult reports whether a branch was committed or still needs explicit
// confirmation because the superseded suffix may have changed the project tree.
type BranchResult struct {
	Event     event.Event
	Effects   []event.BranchEffect
	HeadSeq   event.Seq
	FirstUser bool
	Applied   bool
}

// MemLog 是内存版事件日志:按会话保存有序事件,分配全局单调序号。
type MemLog struct {
	mu          sync.RWMutex
	seq         event.Seq
	events      map[string][]event.Event // session -> 有序事件
	checkpoints map[string]compaction.Checkpoint
	deleted     map[string]struct{} // 已删除会话,其迟到事件一律丢弃
}

// NewMemLog 创建内存日志。
func NewMemLog() *MemLog {
	return &MemLog{
		events:      make(map[string][]event.Event),
		checkpoints: make(map[string]compaction.Checkpoint),
		deleted:     make(map[string]struct{}),
	}
}

// Append 追加事件,分配单调递增序号。
// 会话已被删除时(如删除后回合收尾的迟到事件),直接丢弃、不分配序号,
// 避免已删会话的日志被僵尸事件重建。
func (l *MemLog) Append(ctx context.Context, ev event.Event) (event.Seq, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, gone := l.deleted[ev.Session]; gone {
		return l.seq, nil
	}
	l.seq++
	ev.Seq = l.seq
	l.events[ev.Session] = append(l.events[ev.Session], ev)
	return ev.Seq, nil
}

// Read 返回某会话中序号大于 after 的事件(用于 SSE 补发)。
func (l *MemLog) Read(ctx context.Context, session string, after event.Seq) ([]event.Event, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var out []event.Event
	for _, ev := range l.events[session] {
		if ev.Seq > after {
			out = append(out, ev)
		}
	}
	return out, nil
}

// Delete 清除某会话的全部事件并标记为已删除(删除会话时调用)。
// 标记后该会话的迟到事件(如回合收尾)在 Append 处被丢弃,不会重建分区。
func (l *MemLog) Delete(session string) {
	l.mu.Lock()
	delete(l.events, session)
	delete(l.checkpoints, session)
	l.deleted[session] = struct{}{}
	l.mu.Unlock()
}

// History projects the active conversation branch. Superseded messages remain
// in the event log but are omitted from this client-visible projection.
func (l *MemLog) History(ctx context.Context, session string) ([]message.Message, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var msgs []message.Message
	for _, ev := range activeMessageEvents(l.events[session]) {
		if m, ok := messageFromEvent(ev); ok {
			m.EventSeq = uint64(ev.Seq)
			msgs = append(msgs, m)
		}
	}
	return msgs, nil
}

// ModelHistory returns the provider-visible projection. A valid checkpoint
// replaces its covered immutable prefix; the canonical event log is unchanged.
func (l *MemLog) ModelHistory(ctx context.Context, session string) ([]message.Message, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	events := activeMessageEvents(l.events[session])
	checkpoint, ok := l.checkpoints[session]
	if !ok {
		return compaction.Project(events, nil), nil
	}
	return compaction.Project(events, &checkpoint), nil
}

// Events returns the active message events used for compaction planning.
func (l *MemLog) Events(ctx context.Context, session string) ([]event.Event, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return activeMessageEvents(l.events[session]), nil
}

// Checkpoint returns the latest accepted checkpoint for a session.
func (l *MemLog) Checkpoint(session string) (*compaction.Checkpoint, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	checkpoint, ok := l.checkpoints[session]
	if !ok {
		return nil, false
	}
	return &checkpoint, true
}

// RecordCheckpoint atomically validates a projection, appends its durable event,
// and updates the replay index. The event log remains the source of truth.
func (l *MemLog) RecordCheckpoint(
	ctx context.Context,
	checkpoint compaction.Checkpoint,
) (event.Event, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	sourceEvents := l.events[checkpoint.SessionID]
	active := activeMessageEvents(sourceEvents)
	if !compaction.ValidateCheckpoint(active, checkpoint) {
		return event.Event{}, fmt.Errorf("compaction checkpoint does not match source events")
	}
	l.seq++
	ev := event.Event{
		Seq:     l.seq,
		Kind:    event.KindCompactionCompleted,
		Session: checkpoint.SessionID,
		Payload: checkpoint,
		Time:    checkpoint.CreatedAt,
	}
	l.events[checkpoint.SessionID] = append(sourceEvents, ev)
	l.checkpoints[checkpoint.SessionID] = checkpoint
	return ev, nil
}

// Branch replaces the active suffix beginning at targetUserSeq with a new
// branch marker. No source event is deleted. If the suffix contains possible
// project side effects, callers must explicitly allow them to remain.
func (l *MemLog) Branch(
	ctx context.Context,
	session string,
	targetUserSeq event.Seq,
	editedContent string,
	allowEffects bool,
	expectedHeadSeq event.Seq,
) (BranchResult, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	active := activeMessageEvents(l.events[session])
	targetIndex := -1
	var target message.Message
	for i, ev := range active {
		if ev.Seq != targetUserSeq {
			continue
		}
		msg, ok := messageFromEvent(ev)
		if !ok || msg.Role != message.RoleUser {
			break
		}
		targetIndex = i
		target = msg
		break
	}
	if targetIndex < 0 {
		return BranchResult{}, ErrActiveUserMessageNotFound
	}
	if target.Content == editedContent {
		return BranchResult{}, ErrMessageUnchanged
	}

	effects := branchEffects(active[targetIndex:])
	firstUser := true
	for _, ev := range active[:targetIndex] {
		msg, ok := messageFromEvent(ev)
		if ok && msg.Role == message.RoleUser {
			firstUser = false
			break
		}
	}
	var headSeq event.Seq
	if len(active) > 0 {
		headSeq = active[len(active)-1].Seq
	}
	if len(effects) > 0 && !allowEffects {
		return BranchResult{Effects: effects, HeadSeq: headSeq}, nil
	}
	if len(effects) > 0 && expectedHeadSeq != headSeq {
		return BranchResult{}, ErrBranchChanged
	}

	l.seq++
	payload := event.HistoryBranched{
		TargetUserSeq: targetUserSeq,
		Effects:       effects,
	}
	ev := event.Event{
		Seq:     l.seq,
		Kind:    event.KindHistoryBranched,
		Session: session,
		Time:    time.Now(),
		Payload: payload,
	}
	l.events[session] = append(l.events[session], ev)
	delete(l.checkpoints, session)
	return BranchResult{
		Event:     ev,
		Effects:   effects,
		HeadSeq:   headSeq,
		FirstUser: firstUser,
		Applied:   true,
	}, nil
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
