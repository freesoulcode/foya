package backend

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/freesoulcode/foya/internal/artifact"
	"github.com/freesoulcode/foya/internal/compaction"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/queue"
	"github.com/freesoulcode/foya/internal/session"
	"github.com/freesoulcode/foya/internal/state"
)

var (
	// ErrQueuedMessageNotFound indicates that a queue item no longer exists.
	ErrQueuedMessageNotFound = errors.New("queued message not found")
	// ErrEmptyMessage rejects queue items that contain only whitespace.
	ErrEmptyMessage = errors.New("message must not be empty")
	// ErrInvalidQueuePosition indicates an out-of-range zero-based position.
	ErrInvalidQueuePosition = errors.New("invalid queue position")
	// ErrSessionBusy indicates that a mutually exclusive session operation is active.
	ErrSessionBusy = errors.New("session is busy")
	// ErrActiveUserMessageNotFound indicates that an edit target is not on the active branch.
	ErrActiveUserMessageNotFound = errors.New("active user message not found")
	// ErrMessageUnchanged rejects edits that do not change the user message.
	ErrMessageUnchanged = errors.New("edited message is unchanged")
	// ErrSessionQueueNotEmpty avoids carrying prompts authored against a superseded branch.
	ErrSessionQueueNotEmpty = errors.New("session has queued messages")
	// ErrHistoryChanged rejects a stale side-effect confirmation.
	ErrHistoryChanged = errors.New("active history changed after edit confirmation")
	// ErrAttachmentEditUnsupported avoids silently dropping attachments from an edited turn.
	ErrAttachmentEditUnsupported = errors.New("messages with attachments cannot be edited")
)

const (
	SubmissionStarted        = "started"
	SubmissionQueued         = "queued"
	EditStarted              = "started"
	EditConfirmationRequired = "confirmation_required"
)

// Submission describes whether a submitted message started or was queued.
type Submission struct {
	Status string
	Queued *queue.Message
}

// EditSubmission reports whether an edited turn started or needs explicit
// confirmation that project effects from the old branch will remain.
type EditSubmission struct {
	Status  string
	Effects []event.BranchEffect
	HeadSeq event.Seq
}

type sessionRunner struct {
	cancel           context.CancelFunc
	done             chan struct{}
	stopAfterCurrent bool
}

type turnScheduler struct {
	mu         sync.Mutex
	queues     map[string][]queue.Message
	runners    map[string]*sessionRunner
	compacting map[string]bool
	deleted    map[string]struct{}
}

func newTurnScheduler() *turnScheduler {
	return &turnScheduler{
		queues:     make(map[string][]queue.Message),
		runners:    make(map[string]*sessionRunner),
		compacting: make(map[string]bool),
		deleted:    make(map[string]struct{}),
	}
}

// SubmitTurn starts immediately when the session is idle. While a turn is
// active, it atomically appends to that session's FIFO queue.
func (b *Backend) SubmitTurn(ctx context.Context, sessionID, text string) (Submission, error) {
	return b.SubmitInput(ctx, sessionID, message.UserInput{Text: text})
}

// SubmitInput starts or queues one structured user input.
func (b *Backend) SubmitInput(
	ctx context.Context,
	sessionID string,
	input message.UserInput,
) (Submission, error) {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return Submission{}, session.ErrNotFound
	}
	input.Text = strings.TrimSpace(input.Text)
	if input.Text == "" && len(input.Attachments) == 0 {
		return Submission{}, ErrEmptyMessage
	}
	attachments, err := b.resolveAttachments(ctx, sessionID, input.Attachments)
	if err != nil {
		return Submission{}, err
	}
	input.Attachments = attachments

	b.turns.mu.Lock()
	if _, deleted := b.turns.deleted[sessionID]; deleted {
		b.turns.mu.Unlock()
		return Submission{}, session.ErrNotFound
	}
	if _, running := b.turns.runners[sessionID]; running || b.turns.compacting[sessionID] {
		item := queue.NewMessage(sessionID, input, len(b.turns.queues[sessionID]))
		b.turns.queues[sessionID] = append(b.turns.queues[sessionID], item)
		b.broadcastQueueLocked(sessionID)
		b.turns.mu.Unlock()
		return Submission{Status: SubmissionQueued, Queued: &item}, nil
	}

	// A queue can remain while idle after the user explicitly stops a turn.
	// Preserve FIFO: append the new message, then resume with the oldest item.
	if len(b.turns.queues[sessionID]) > 0 {
		item := queue.NewMessage(sessionID, input, len(b.turns.queues[sessionID]))
		b.turns.queues[sessionID] = append(b.turns.queues[sessionID], item)
		next := b.popQueueLocked(sessionID)
		runner, turnCtx := b.createRunnerLocked(sessionID)
		b.broadcastQueueLocked(sessionID)
		b.turns.mu.Unlock()
		go b.runTurnLoop(sessionID, queueInput(next), runner, turnCtx, false)
		return Submission{Status: SubmissionQueued, Queued: &item}, nil
	}

	runner, turnCtx := b.createRunnerLocked(sessionID)
	b.turns.mu.Unlock()
	go b.runTurnLoop(sessionID, input, runner, turnCtx, false)
	return Submission{Status: SubmissionStarted}, nil
}

func (b *Backend) resolveAttachments(
	ctx context.Context,
	sessionID string,
	refs []message.AttachmentRef,
) ([]message.AttachmentRef, error) {
	if len(refs) > artifact.MaxAttachments {
		return nil, fmt.Errorf("at most %d attachments are allowed", artifact.MaxAttachments)
	}
	if len(refs) == 0 {
		return nil, nil
	}
	b.mu.RLock()
	store := b.artifacts
	b.mu.RUnlock()
	if store == nil {
		return nil, errors.New("artifact store is unavailable")
	}
	out := make([]message.AttachmentRef, 0, len(refs))
	var total int64
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		if _, duplicate := seen[ref.ID]; duplicate {
			return nil, errors.New("duplicate attachment")
		}
		seen[ref.ID] = struct{}{}
		_, canonical, err := store.Read(ctx, sessionID, ref.ID)
		if err != nil {
			return nil, fmt.Errorf("invalid attachment %q: %w", ref.ID, err)
		}
		total += canonical.Bytes
		if total > artifact.MaxTurnBytes {
			return nil, fmt.Errorf("attachments exceed %d bytes", artifact.MaxTurnBytes)
		}
		out = append(out, canonical)
	}
	ids := make([]string, 0, len(out))
	for _, ref := range out {
		ids = append(ids, ref.ID)
	}
	if err := store.Commit(ctx, sessionID, ids); err != nil {
		return nil, err
	}
	return out, nil
}

// EnqueueMessage explicitly appends an item without starting a turn.
func (b *Backend) EnqueueMessage(ctx context.Context, sessionID, text string) (queue.Message, error) {
	return b.EnqueueInput(ctx, sessionID, message.UserInput{Text: text})
}

func (b *Backend) EnqueueInput(
	ctx context.Context,
	sessionID string,
	input message.UserInput,
) (queue.Message, error) {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return queue.Message{}, session.ErrNotFound
	}
	input.Text = strings.TrimSpace(input.Text)
	if input.Text == "" && len(input.Attachments) == 0 {
		return queue.Message{}, ErrEmptyMessage
	}
	attachments, err := b.resolveAttachments(ctx, sessionID, input.Attachments)
	if err != nil {
		return queue.Message{}, err
	}
	input.Attachments = attachments

	b.turns.mu.Lock()
	if _, deleted := b.turns.deleted[sessionID]; deleted {
		b.turns.mu.Unlock()
		return queue.Message{}, session.ErrNotFound
	}
	item := queue.NewMessage(sessionID, input, len(b.turns.queues[sessionID]))
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
	if b.turns.compacting[sessionID] {
		if index > 0 {
			items = append(items[:index], items[index+1:]...)
			items = append([]queue.Message{selected}, items...)
			normalizePositions(items)
			b.turns.queues[sessionID] = items
		}
		selected = b.turns.queues[sessionID][0]
		b.broadcastQueueLocked(sessionID)
		b.turns.mu.Unlock()
		b.engine.Cancel(sessionID)
		return selected, nil
	}

	items = append(items[:index], items[index+1:]...)
	normalizePositions(items)
	b.setQueueLocked(sessionID, items)
	runner, turnCtx := b.createRunnerLocked(sessionID)
	b.broadcastQueueLocked(sessionID)
	b.turns.mu.Unlock()
	go b.runTurnLoop(sessionID, queueInput(selected), runner, turnCtx, false)
	return selected, nil
}

// cancelCurrentTurn stops the active turn and pauses automatic queue draining.
func (b *Backend) cancelCurrentTurn(sessionID string) {
	b.turns.mu.Lock()
	runner := b.turns.runners[sessionID]
	compacting := b.turns.compacting[sessionID]
	if runner != nil {
		runner.stopAfterCurrent = true
	}
	b.turns.mu.Unlock()
	if runner != nil {
		runner.cancel()
		return
	}
	if compacting {
		b.engine.Cancel(sessionID)
	}
}

// CompactSession runs one standalone compaction. New turns submitted while it
// runs are queued and start automatically after the compaction settles.
func (b *Backend) CompactSession(
	ctx context.Context,
	sessionID string,
) (*compaction.Checkpoint, error) {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return nil, session.ErrNotFound
	}
	b.turns.mu.Lock()
	if _, deleted := b.turns.deleted[sessionID]; deleted {
		b.turns.mu.Unlock()
		return nil, session.ErrNotFound
	}
	if b.turns.runners[sessionID] != nil || b.turns.compacting[sessionID] {
		b.turns.mu.Unlock()
		return nil, ErrSessionBusy
	}
	b.turns.compacting[sessionID] = true
	b.turns.mu.Unlock()

	checkpoint, err := b.engine.CompactSession(ctx, sessionID)

	b.turns.mu.Lock()
	delete(b.turns.compacting, sessionID)
	if _, deleted := b.turns.deleted[sessionID]; deleted ||
		len(b.turns.queues[sessionID]) == 0 {
		b.turns.mu.Unlock()
		return checkpoint, err
	}
	next := b.popQueueLocked(sessionID)
	runner, turnCtx := b.createRunnerLocked(sessionID)
	b.broadcastQueueLocked(sessionID)
	b.turns.mu.Unlock()
	go b.runTurnLoop(sessionID, queueInput(next), runner, turnCtx, false)
	return checkpoint, err
}

// EditTurn creates a new active history branch before targetUserSeq and starts
// the replacement user turn. Superseded events and project changes are kept.
func (b *Backend) EditTurn(
	ctx context.Context,
	sessionID string,
	targetUserSeq event.Seq,
	text string,
	confirmEffects bool,
	expectedHeadSeq event.Seq,
) (EditSubmission, error) {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return EditSubmission{}, session.ErrNotFound
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return EditSubmission{}, ErrEmptyMessage
	}
	history, err := b.log.History(ctx, sessionID)
	if err != nil {
		return EditSubmission{}, err
	}
	for _, item := range history {
		if item.EventSeq == uint64(targetUserSeq) && len(item.Attachments) > 0 {
			return EditSubmission{}, ErrAttachmentEditUnsupported
		}
	}

	b.turns.mu.Lock()
	if _, deleted := b.turns.deleted[sessionID]; deleted {
		b.turns.mu.Unlock()
		return EditSubmission{}, session.ErrNotFound
	}
	if b.turns.runners[sessionID] != nil || b.turns.compacting[sessionID] {
		b.turns.mu.Unlock()
		return EditSubmission{}, ErrSessionBusy
	}
	if len(b.turns.queues[sessionID]) > 0 {
		b.turns.mu.Unlock()
		return EditSubmission{}, ErrSessionQueueNotEmpty
	}

	branch, err := b.log.Branch(
		ctx,
		sessionID,
		targetUserSeq,
		text,
		confirmEffects,
		expectedHeadSeq,
	)
	if err != nil {
		b.turns.mu.Unlock()
		switch {
		case errors.Is(err, state.ErrActiveUserMessageNotFound):
			return EditSubmission{}, ErrActiveUserMessageNotFound
		case errors.Is(err, state.ErrMessageUnchanged):
			return EditSubmission{}, ErrMessageUnchanged
		case errors.Is(err, state.ErrBranchChanged):
			return EditSubmission{}, ErrHistoryChanged
		default:
			return EditSubmission{}, err
		}
	}
	if !branch.Applied {
		b.turns.mu.Unlock()
		return EditSubmission{
			Status:  EditConfirmationRequired,
			Effects: branch.Effects,
			HeadSeq: branch.HeadSeq,
		}, nil
	}

	b.engine.InvalidateHistoryEstimate(sessionID)
	runner, turnCtx := b.createRunnerLocked(sessionID)
	publishCtx := context.WithoutCancel(ctx)
	_ = b.bus.PublishMustDeliver(
		publishCtx,
		"session:"+sessionID,
		branch.Event,
	)
	if branch.FirstUser {
		if updated, changed, resetErr := b.sessions.ResetGeneratedTitle(sessionID); resetErr == nil && changed {
			b.broadcastSession(publishCtx, updated)
		}
	}
	b.turns.mu.Unlock()

	go b.runTurnLoop(sessionID, message.UserInput{Text: text}, runner, turnCtx, true)
	return EditSubmission{
		Status:  EditStarted,
		Effects: branch.Effects,
		HeadSeq: branch.HeadSeq,
	}, nil
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
	sessionID string,
	input message.UserInput,
	runner *sessionRunner,
	turnCtx context.Context,
	editedHistory bool,
) {
	defer close(runner.done)
	for {
		if editedHistory {
			_ = b.engine.RunEditedTurn(turnCtx, sessionID, input.Text)
			editedHistory = false
		} else {
			_ = b.engine.RunInput(turnCtx, sessionID, input)
		}

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
		input = queueInput(next)
	}
}

func queueInput(item queue.Message) message.UserInput {
	return message.UserInput{
		Text:        item.Text,
		Attachments: append([]message.AttachmentRef(nil), item.Attachments...),
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
