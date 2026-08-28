// 内存版事件日志与历史投影(脚手架阶段;后续由 JSONL + SQLite 替换)。
package state

import (
	"context"
	"fmt"
	"sync"

	"github.com/freesoulcode/foya/internal/compaction"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/message"
)

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

// History 从事件日志投影出某会话的对话历史(多轮上下文的真相来源)。
// 取所有 MessageEnd 事件,按序还原为消息序列。
func (l *MemLog) History(ctx context.Context, session string) ([]message.Message, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var msgs []message.Message
	for _, ev := range l.events[session] {
		if ev.Kind != event.KindMessageEnd {
			continue
		}
		if m, ok := ev.Payload.(message.Message); ok {
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
	events := append([]event.Event(nil), l.events[session]...)
	checkpoint, ok := l.checkpoints[session]
	if !ok {
		return compaction.Project(events, nil), nil
	}
	return compaction.Project(events, &checkpoint), nil
}

// Events returns an ordered snapshot for compaction planning.
func (l *MemLog) Events(ctx context.Context, session string) ([]event.Event, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return append([]event.Event(nil), l.events[session]...), nil
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
	events := l.events[checkpoint.SessionID]
	if !compaction.ValidateCheckpoint(events, checkpoint) {
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
	l.events[checkpoint.SessionID] = append(events, ev)
	l.checkpoints[checkpoint.SessionID] = checkpoint
	return ev, nil
}
