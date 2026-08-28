package backend

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/queue"
	"github.com/freesoulcode/foya/internal/session"
)

var (
	// ErrQueuedMessageNotFound indicates that a queue item no longer exists.
	ErrQueuedMessageNotFound = errors.New("queued message not found")
	// ErrEmptyMessage rejects queue items that contain only whitespace.
	ErrEmptyMessage = errors.New("message must not be empty")
	// ErrInvalidQueuePosition indicates an out-of-range zero-based position.
	ErrInvalidQueuePosition = errors.New("invalid queue position")
)

const (
	SubmissionStarted = "started"
	SubmissionQueued  = "queued"
)

// Submission describes whether a submitted message started or was queued.
type Submission struct {
	Status string
	Queued *queue.Message
}

type sessionRunner struct {
	cancel           context.CancelFunc
	done             chan struct{}
	stopAfterCurrent bool
}

type turnScheduler struct {
	mu      sync.Mutex
	queues  map[string][]queue.Message
	runners map[string]*sessionRunner
	deleted map[string]struct{}
}

func newTurnScheduler() *turnScheduler {
	return &turnScheduler{
		queues:  make(map[string][]queue.Message),
		runners: make(map[string]*sessionRunner),
		deleted: make(map[string]struct{}),
	}
}

// SubmitTurn starts immediately when the session is idle. While a turn is
// active, it atomically appends to that session's FIFO queue.
func (b *Backend) SubmitTurn(ctx context.Context, sessionID, text string) (Submission, error) {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return Submission{}, session.ErrNotFound
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return Submission{}, ErrEmptyMessage
	}

	b.turns.mu.Lock()
	if _, deleted := b.turns.deleted[sessionID]; deleted {
		b.turns.mu.Unlock()
		return Submission{}, session.ErrNotFound
	}
	if _, running := b.turns.runners[sessionID]; running {
		item := queue.NewMessage(sessionID, text, len(b.turns.queues[sessionID]))
		b.turns.queues[sessionID] = append(b.turns.queues[sessionID], item)
		b.broadcastQueueLocked(sessionID)
		b.turns.mu.Unlock()
		return Submission{Status: SubmissionQueued, Queued: &item}, nil
	}

	// A queue can remain while idle after the user explicitly stops a turn.
	// Preserve FIFO: append the new message, then resume with the oldest item.
	if len(b.turns.queues[sessionID]) > 0 {
		item := queue.NewMessage(sessionID, text, len(b.turns.queues[sessionID]))
		b.turns.queues[sessionID] = append(b.turns.queues[sessionID], item)
		next := b.popQueueLocked(sessionID)
		runner, turnCtx := b.createRunnerLocked(sessionID)
		b.broadcastQueueLocked(sessionID)
		b.turns.mu.Unlock()
		go b.runTurnLoop(sessionID, next.Text, runner, turnCtx)
		return Submission{Status: SubmissionQueued, Queued: &item}, nil
	}

	runner, turnCtx := b.createRunnerLocked(sessionID)
	b.turns.mu.Unlock()
	go b.runTurnLoop(sessionID, text, runner, turnCtx)
	return Submission{Status: SubmissionStarted}, nil
}

// EnqueueMessage explicitly appends an item without starting a turn.
func (b *Backend) EnqueueMessage(ctx context.Context, sessionID, text string) (queue.Message, error) {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return queue.Message{}, session.ErrNotFound
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return queue.Message{}, ErrEmptyMessage
	}

	b.turns.mu.Lock()
	if _, deleted := b.turns.deleted[sessionID]; deleted {
		b.turns.mu.Unlock()
		return queue.Message{}, session.ErrNotFound
	}
	item := queue.NewMessage(sessionID, text, len(b.turns.queues[sessionID]))
	b.turns.queues[sessionID] = append(b.turns.queues[sessionID], item)
	b.broadcastQueueLocked(sessionID)
	b.turns.mu.Unlock()
	return item, nil
}

// ListQueuedMessages returns an ordered copy of a session's queue.
func (b *Backend) ListQueuedMessages(sessionID string) ([]queue.Message, error) {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return nil, session.ErrNotFound
	}
	b.turns.mu.Lock()
	defer b.turns.mu.Unlock()
	if _, deleted := b.turns.deleted[sessionID]; deleted {
		return nil, session.ErrNotFound
	}
	return cloneQueue(b.turns.queues[sessionID]), nil
}

// UpdateQueuedMessage edits text and/or moves an item to a zero-based position.
func (b *Backend) UpdateQueuedMessage(
	ctx context.Context,
	sessionID, messageID string,
	text *string,
	position *int,
) (queue.Message, error) {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return queue.Message{}, session.ErrNotFound
	}

	b.turns.mu.Lock()
	defer b.turns.mu.Unlock()
	if _, deleted := b.turns.deleted[sessionID]; deleted {
		return queue.Message{}, session.ErrNotFound
	}

	items := b.turns.queues[sessionID]
	index := queueIndex(items, messageID)
	if index < 0 {
		return queue.Message{}, ErrQueuedMessageNotFound
	}
	if position != nil && (*position < 0 || *position >= len(items)) {
		return queue.Message{}, ErrInvalidQueuePosition
	}
	if text != nil {
		trimmed := strings.TrimSpace(*text)
		if trimmed == "" {
			return queue.Message{}, ErrEmptyMessage
		}
		items[index].Text = trimmed
		items[index].UpdatedAt = time.Now()
	}
	if position != nil && *position != index {
		item := items[index]
		item.UpdatedAt = time.Now()
		items = append(items[:index], items[index+1:]...)
		target := *position
		items = append(items, queue.Message{})
		copy(items[target+1:], items[target:])
		items[target] = item
		index = target
	}
	normalizePositions(items)
	b.turns.queues[sessionID] = items
	b.broadcastQueueLocked(sessionID)
	return items[index], nil
}

// DeleteQueuedMessage removes one pending item.
func (b *Backend) DeleteQueuedMessage(ctx context.Context, sessionID, messageID string) error {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return session.ErrNotFound
	}

	b.turns.mu.Lock()
	defer b.turns.mu.Unlock()
	if _, deleted := b.turns.deleted[sessionID]; deleted {
		return session.ErrNotFound
	}
	items := b.turns.queues[sessionID]
	index := queueIndex(items, messageID)
	if index < 0 {
		return ErrQueuedMessageNotFound
	}
	items = append(items[:index], items[index+1:]...)
	normalizePositions(items)
	b.setQueueLocked(sessionID, items)
	b.broadcastQueueLocked(sessionID)
	return nil
}

// DispatchQueuedMessage starts an idle item immediately. If a turn is active,
// the selected item becomes queue head and the active turn is cancelled; the
// runner starts the selected item as soon as cancellation has completed.
func (b *Backend) DispatchQueuedMessage(
	ctx context.Context,
	sessionID, messageID string,
) (queue.Message, error) {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return queue.Message{}, session.ErrNotFound
	}

	b.turns.mu.Lock()
	if _, deleted := b.turns.deleted[sessionID]; deleted {
		b.turns.mu.Unlock()
		return queue.Message{}, session.ErrNotFound
	}
	items := b.turns.queues[sessionID]
	index := queueIndex(items, messageID)
	if index < 0 {
		b.turns.mu.Unlock()
		return queue.Message{}, ErrQueuedMessageNotFound
	}
	selected := items[index]

	if runner := b.turns.runners[sessionID]; runner != nil {
		if index > 0 {
			items = append(items[:index], items[index+1:]...)
			items = append([]queue.Message{selected}, items...)
			normalizePositions(items)
			b.turns.queues[sessionID] = items
		}
		selected = b.turns.queues[sessionID][0]
		runner.stopAfterCurrent = false
		cancel := runner.cancel
		b.broadcastQueueLocked(sessionID)
		b.turns.mu.Unlock()
		cancel()
		return selected, nil
	}

	items = append(items[:index], items[index+1:]...)
	normalizePositions(items)
	b.setQueueLocked(sessionID, items)
	runner, turnCtx := b.createRunnerLocked(sessionID)
	b.broadcastQueueLocked(sessionID)
	b.turns.mu.Unlock()
	go b.runTurnLoop(sessionID, selected.Text, runner, turnCtx)
	return selected, nil
}

// cancelCurrentTurn stops the active turn and pauses automatic queue draining.
func (b *Backend) cancelCurrentTurn(sessionID string) {
	b.turns.mu.Lock()
	runner := b.turns.runners[sessionID]
	if runner != nil {
		runner.stopAfterCurrent = true
	}
	b.turns.mu.Unlock()
	if runner != nil {
		runner.cancel()
	}
}

func (b *Backend) stopSessionAndWait(sessionID string, timeout time.Duration) {
	b.turns.mu.Lock()
	b.turns.deleted[sessionID] = struct{}{}
	delete(b.turns.queues, sessionID)
	runner := b.turns.runners[sessionID]
	if runner != nil {
		runner.stopAfterCurrent = true
	}
	b.turns.mu.Unlock()
	if runner == nil {
		b.engine.CancelAndWait(sessionID, timeout)
		return
	}
	runner.cancel()
	select {
	case <-runner.done:
	case <-time.After(timeout):
	}
}

func (b *Backend) createRunnerLocked(sessionID string) (*sessionRunner, context.Context) {
	ctx, cancel := context.WithCancel(context.Background())
	runner := &sessionRunner{cancel: cancel, done: make(chan struct{})}
	b.turns.runners[sessionID] = runner
	return runner, ctx
}

func (b *Backend) runTurnLoop(
	sessionID, text string,
	runner *sessionRunner,
	turnCtx context.Context,
) {
	defer close(runner.done)
	for {
		_ = b.engine.RunTurn(turnCtx, sessionID, text)

		b.turns.mu.Lock()
		current, exists := b.turns.runners[sessionID]
		if !exists || current != runner {
			b.turns.mu.Unlock()
			return
		}
		if runner.stopAfterCurrent {
			delete(b.turns.runners, sessionID)
			b.turns.mu.Unlock()
			return
		}
		if _, exists := b.sessions.Get(sessionID); !exists {
			delete(b.turns.runners, sessionID)
			b.turns.mu.Unlock()
			return
		}
		if len(b.turns.queues[sessionID]) == 0 {
			delete(b.turns.runners, sessionID)
			b.turns.mu.Unlock()
			return
		}

		next := b.popQueueLocked(sessionID)
		turnCtx, runner.cancel = context.WithCancel(context.Background())
		b.broadcastQueueLocked(sessionID)
		b.turns.mu.Unlock()
		text = next.Text
	}
}

func (b *Backend) popQueueLocked(sessionID string) queue.Message {
	items := b.turns.queues[sessionID]
	next := items[0]
	items = items[1:]
	normalizePositions(items)
	b.setQueueLocked(sessionID, items)
	return next
}

func (b *Backend) setQueueLocked(sessionID string, items []queue.Message) {
	if len(items) == 0 {
		delete(b.turns.queues, sessionID)
		return
	}
	b.turns.queues[sessionID] = items
}

func (b *Backend) broadcastQueueLocked(sessionID string) {
	snapshot := queue.Snapshot{Items: cloneQueue(b.turns.queues[sessionID])}
	ev := event.Event{
		Kind:    event.KindQueueUpdated,
		Session: sessionID,
		Time:    time.Now(),
		Payload: snapshot,
	}
	ctx := context.Background()
	seq, _ := b.log.Append(ctx, ev)
	ev.Seq = seq
	_ = b.bus.PublishMustDeliver(ctx, "session:"+sessionID, ev)
}

func cloneQueue(items []queue.Message) []queue.Message {
	out := make([]queue.Message, len(items))
	copy(out, items)
	return out
}

func queueIndex(items []queue.Message, id string) int {
	for i := range items {
		if items[i].ID == id {
			return i
		}
	}
	return -1
}

func normalizePositions(items []queue.Message) {
	for i := range items {
		items[i].Position = i
	}
}
