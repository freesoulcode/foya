package backend

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/freesoulcode/foya/internal/agent"
	"github.com/freesoulcode/foya/internal/artifact"
	"github.com/freesoulcode/foya/internal/compaction"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/queue"
	"github.com/freesoulcode/foya/internal/session"
	"github.com/freesoulcode/foya/internal/state"
	foyatelemetry "github.com/freesoulcode/foya/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
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
	// ErrActiveUserMessageNotFound indicates that a user-message target is not in active history.
	ErrActiveUserMessageNotFound = errors.New("active user message not found")
	// ErrSessionQueueNotEmpty avoids carrying prompts authored against superseded history.
	ErrSessionQueueNotEmpty = errors.New("session has queued messages")
	// ErrActiveMessageNotFound indicates that a requested fork boundary is not active.
	ErrActiveMessageNotFound = errors.New("active message not found")
	// ErrInvalidForkBoundary indicates that the requested fork boundary is not a complete assistant/tool point.
	ErrInvalidForkBoundary = errors.New("invalid fork boundary")
	// ErrHistoryChanged rejects a stale history-change confirmation.
	ErrHistoryChanged = errors.New("active history changed after confirmation")
	// ErrFileStateChanged rejects confirmation against a stale file preview.
	ErrFileStateChanged = errors.New("file state changed after rewind preview")
	// ErrFileReviewChanged rejects an action against a stale pending-review set.
	ErrFileReviewChanged = errors.New("pending file changes changed after review preview")
	// ErrNoPendingFileChanges indicates that the review queue is empty.
	ErrNoPendingFileChanges = errors.New("no pending file changes")
	// ErrFileRewindConflict avoids overwriting files changed after the recorded edit.
	ErrFileRewindConflict = errors.New("file changed after the recorded edit")
	// ErrFileRewindFailed indicates that a verified file could not be restored.
	ErrFileRewindFailed = errors.New("file rewind failed")
	// ErrRewindContextUnsupported avoids silently dropping non-text user context.
	ErrRewindContextUnsupported = errors.New("messages with attachments, browser context or commands cannot be moved back to composer")
	// ErrImageInputUnsupported rejects image input unless the selected connection
	// explicitly declares visual input support.
	ErrImageInputUnsupported = errors.New("selected model does not support image input")
	// ErrToolCallNotRunning means the requested tool has already completed or
	// does not belong to the session.
	ErrToolCallNotRunning = errors.New("tool call is not running")
)

const (
	SubmissionStarted        = "started"
	SubmissionQueued         = "queued"
	RewindApplied            = "rewound"
	RewindConfirmationNeeded = "confirmation_required"
	RewindFileReady          = "ready"
	RewindFileMergeable      = "mergeable"
	RewindFileModified       = "modified"
	maxBrowserElements       = 8
	maxBrowserElementBytes   = 40 * 1024
	maxBrowserContextBytes   = 96 * 1024
)

// Submission describes whether a submitted message started or was queued.
type Submission struct {
	Status string
	Queued *queue.Message
}

// RewindSubmission reports whether a user message can be moved back to the
// composer or whether the caller must confirm first.
type RewindSubmission struct {
	Status         string
	Message        string
	Files          []RewindFilePreview
	FileStateToken string
	HeadSeq        event.Seq
}

type RewindFilePreview struct {
	Key       string
	Path      string
	Status    string
	Additions int
	Deletions int
	Diff      string
}

type sessionRunner struct {
	cancel           context.CancelCauseFunc
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

// SubmitChatInput exposes message submission to platform-neutral chat adapters.
func (b *Backend) SubmitChatInput(
	ctx context.Context,
	sessionID string,
	input message.UserInput,
) error {
	_, err := b.SubmitInput(ctx, sessionID, input)
	return err
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
	if input.Text == "" && len(input.Attachments) == 0 && len(input.BrowserElements) == 0 {
		return Submission{}, ErrEmptyMessage
	}
	browserElements, err := normalizeBrowserElements(input.BrowserElements)
	if err != nil {
		return Submission{}, err
	}
	input.BrowserElements = browserElements
	if err := b.validateImageInput(sessionID, input.Attachments); err != nil {
		return Submission{}, err
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
		go b.runTurnLoop(sessionID, queueInput(next), next.CreatedAt, runner, turnCtx)
		return Submission{Status: SubmissionQueued, Queued: &item}, nil
	}

	runner, turnCtx := b.createRunnerLocked(sessionID)
	b.turns.mu.Unlock()
	go b.runTurnLoop(sessionID, input, time.Time{}, runner, turnCtx)
	return Submission{Status: SubmissionStarted}, nil
}

func normalizeBrowserElements(
	elements []message.BrowserElement,
) ([]message.BrowserElement, error) {
	if len(elements) > maxBrowserElements {
		return nil, fmt.Errorf("at most %d browser elements are allowed", maxBrowserElements)
	}
	out := make([]message.BrowserElement, 0, len(elements))
	total := 0
	for _, element := range elements {
		element.PageURL = strings.TrimSpace(element.PageURL)
		element.PageTitle = strings.TrimSpace(element.PageTitle)
		element.Tag = strings.ToLower(strings.TrimSpace(element.Tag))
		element.Selector = strings.TrimSpace(element.Selector)
		element.Text = strings.TrimSpace(element.Text)
		element.HTML = strings.TrimSpace(element.HTML)
		parsed, err := url.ParseRequestURI(element.PageURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return nil, errors.New("browser element URL must use HTTP or HTTPS")
		}
		if element.Tag == "" || element.Selector == "" {
			return nil, errors.New("browser element tag and selector are required")
		}
		size := len(element.PageURL) + len(element.PageTitle) + len(element.Tag) +
			len(element.Selector) + len(element.Text) + len(element.HTML)
		if size > maxBrowserElementBytes {
			return nil, fmt.Errorf(
				"browser element exceeds %d bytes",
				maxBrowserElementBytes,
			)
		}
		total += size
		if total > maxBrowserContextBytes {
			return nil, fmt.Errorf(
				"browser elements exceed %d bytes",
				maxBrowserContextBytes,
			)
		}
		out = append(out, element)
	}
	return out, nil
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
	if input.Text == "" && len(input.Attachments) == 0 && len(input.BrowserElements) == 0 {
		return queue.Message{}, ErrEmptyMessage
	}
	browserElements, err := normalizeBrowserElements(input.BrowserElements)
	if err != nil {
		return queue.Message{}, err
	}
	input.BrowserElements = browserElements
	if err := b.validateImageInput(sessionID, input.Attachments); err != nil {
		return queue.Message{}, err
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

func (b *Backend) validateImageInput(
	sessionID string,
	refs []message.AttachmentRef,
) error {
	// Attachment IDs are client-provided references, so do not trust Kind
	// before resolving them. The current upload surface only creates images.
	if len(refs) > 0 {
		item, ok := b.sessions.Get(sessionID)
		if !ok {
			return session.ErrNotFound
		}
		connection, ok := b.Connection(item.ConnectionID)
		model := item.Model
		settings, configured := connection.ModelSettings[model]
		if !ok || configured && !settings.ImageInputSupported() {
			return ErrImageInputUnsupported
		}
	}
	return nil
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
		b.engine.CancelWithReason(sessionID, agent.TurnCancelReasonQueueDispatch)
		cancel(agent.ErrTurnCancelledByQueueDispatch)
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
		b.engine.CancelWithReason(sessionID, agent.TurnCancelReasonQueueDispatch)
		return selected, nil
	}

	items = append(items[:index], items[index+1:]...)
	normalizePositions(items)
	b.setQueueLocked(sessionID, items)
	runner, turnCtx := b.createRunnerLocked(sessionID)
	b.broadcastQueueLocked(sessionID)
	b.turns.mu.Unlock()
	go b.runTurnLoop(sessionID, queueInput(selected), selected.CreatedAt, runner, turnCtx)
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
		b.engine.CancelWithReason(sessionID, agent.TurnCancelReasonUserStop)
		runner.cancel(agent.ErrTurnCancelledByUser)
		return
	}
	if compacting {
		b.engine.CancelWithReason(sessionID, agent.TurnCancelReasonUserStop)
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
	go b.runTurnLoop(sessionID, queueInput(next), next.CreatedAt, runner, turnCtx)
	return checkpoint, err
}

// RewindTurn moves an active text-only user message back to the composer by
// trimming the active history from that message onward. It does not start a turn.
func (b *Backend) RewindTurn(
	ctx context.Context,
	sessionID string,
	targetUserSeq event.Seq,
	confirm bool,
	expectedHeadSeq event.Seq,
	expectedFileState string,
	forceFileKeys []string,
) (RewindSubmission, error) {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return RewindSubmission{}, session.ErrNotFound
	}
	history, err := b.log.History(ctx, sessionID)
	if err != nil {
		return RewindSubmission{}, err
	}
	found := false
	for _, item := range history {
		if item.EventSeq != uint64(targetUserSeq) {
			continue
		}
		found = true
		if item.Role != message.RoleUser {
			return RewindSubmission{}, ErrActiveUserMessageNotFound
		}
		if item.Command != "" ||
			len(item.Attachments) > 0 ||
			len(item.BrowserElements) > 0 {
			return RewindSubmission{}, ErrRewindContextUnsupported
		}
		break
	}
	if !found {
		return RewindSubmission{}, ErrActiveUserMessageNotFound
	}

	b.turns.mu.Lock()
	if _, deleted := b.turns.deleted[sessionID]; deleted {
		b.turns.mu.Unlock()
		return RewindSubmission{}, session.ErrNotFound
	}
	if b.turns.runners[sessionID] != nil || b.turns.compacting[sessionID] {
		b.turns.mu.Unlock()
		return RewindSubmission{}, ErrSessionBusy
	}
	if len(b.turns.queues[sessionID]) > 0 {
		b.turns.mu.Unlock()
		return RewindSubmission{}, ErrSessionQueueNotEmpty
	}

	rewind, err := b.log.Rewind(ctx, sessionID, targetUserSeq, false, 0, nil, "")
	if err != nil {
		b.turns.mu.Unlock()
		switch {
		case errors.Is(err, state.ErrActiveUserMessageNotFound):
			return RewindSubmission{}, ErrActiveUserMessageNotFound
		default:
			return RewindSubmission{}, err
		}
	}
	candidates, fileStateToken, err := inspectFileRewind(ctx, b.log, rewind.FileChanges)
	if err != nil {
		b.turns.mu.Unlock()
		if errors.Is(err, ErrFileRewindConflict) {
			return RewindSubmission{}, err
		}
		return RewindSubmission{}, fmt.Errorf("%w: %v", ErrFileRewindFailed, err)
	}
	files := b.displayRewindFiles(sessionID, candidates)
	if !confirm {
		b.turns.mu.Unlock()
		return RewindSubmission{
			Status:         RewindConfirmationNeeded,
			Message:        rewind.Message.Content,
			Files:          files,
			FileStateToken: fileStateToken,
			HeadSeq:        rewind.HeadSeq,
		}, nil
	}
	if expectedHeadSeq != rewind.HeadSeq {
		b.turns.mu.Unlock()
		return RewindSubmission{}, ErrHistoryChanged
	}
	if expectedFileState != fileStateToken {
		b.turns.mu.Unlock()
		return RewindSubmission{}, ErrFileStateChanged
	}

	fileResults, plans, err := selectFileRewindPlans(candidates, forceFileKeys)
	if err != nil {
		b.turns.mu.Unlock()
		if errors.Is(err, ErrFileRewindConflict) {
			return RewindSubmission{}, err
		}
		return RewindSubmission{}, fmt.Errorf("%w: %v", ErrFileRewindFailed, err)
	}
	journalID := ""
	if len(plans) > 0 {
		journalID, err = b.log.BeginFileRewind(
			ctx,
			sessionID,
			targetUserSeq,
			expectedHeadSeq,
			fileRewindBackups(plans),
		)
		if err != nil {
			b.turns.mu.Unlock()
			return RewindSubmission{}, fmt.Errorf("%w: %v", ErrFileRewindFailed, err)
		}
	}
	restoreFiles, err := applyFileRewindPlans(plans)
	if err != nil {
		if journalID != "" {
			_ = b.log.FinishFileRewind(context.WithoutCancel(ctx), journalID)
		}
		b.turns.mu.Unlock()
		if errors.Is(err, ErrFileRewindConflict) {
			return RewindSubmission{}, err
		}
		return RewindSubmission{}, fmt.Errorf("%w: %v", ErrFileRewindFailed, err)
	}
	committed, err := b.log.Rewind(
		ctx,
		sessionID,
		targetUserSeq,
		true,
		expectedHeadSeq,
		fileResults,
		journalID,
	)
	if err != nil {
		restoreErr := restoreFiles()
		if journalID != "" {
			_ = b.log.FinishFileRewind(context.WithoutCancel(ctx), journalID)
		}
		b.turns.mu.Unlock()
		if restoreErr != nil {
			return RewindSubmission{}, errors.Join(err, fmt.Errorf("restore files after failed history commit: %w", restoreErr))
		}
		switch {
		case errors.Is(err, state.ErrActiveUserMessageNotFound):
			return RewindSubmission{}, ErrActiveUserMessageNotFound
		case errors.Is(err, state.ErrHistoryChanged):
			return RewindSubmission{}, ErrHistoryChanged
		default:
			return RewindSubmission{}, err
		}
	}
	rewind = committed
	if journalID != "" {
		_ = b.log.FinishFileRewind(context.WithoutCancel(ctx), journalID)
	}

	b.engine.InvalidateHistoryEstimate(sessionID)
	publishCtx := context.WithoutCancel(ctx)
	_ = b.bus.PublishMustDeliver(
		publishCtx,
		"session:"+sessionID,
		rewind.Event,
	)
	if rewind.FirstUser {
		if updated, changed, resetErr := b.sessions.ResetGeneratedTitle(sessionID); resetErr == nil && changed {
			b.broadcastSession(publishCtx, updated)
		}
	}
	b.turns.mu.Unlock()

	return RewindSubmission{
		Status:         RewindApplied,
		Message:        rewind.Message.Content,
		Files:          files,
		FileStateToken: fileStateToken,
		HeadSeq:        rewind.HeadSeq,
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
	b.engine.CancelWithReason(sessionID, agent.TurnCancelReasonSessionDeleted)
	runner.cancel(agent.ErrTurnCancelledBySessionDelete)
	select {
	case <-runner.done:
	case <-time.After(timeout):
	}
}

func (b *Backend) createRunnerLocked(sessionID string) (*sessionRunner, context.Context) {
	ctx, cancel := context.WithCancelCause(context.Background())
	runner := &sessionRunner{cancel: cancel, done: make(chan struct{})}
	b.turns.runners[sessionID] = runner
	return runner, ctx
}

func (b *Backend) runTurnLoop(
	sessionID string,
	input message.UserInput,
	queuedAt time.Time,
	runner *sessionRunner,
	turnCtx context.Context,
) {
	defer close(runner.done)
	for {
		if !queuedAt.IsZero() {
			foyatelemetry.RecordQueueWait(
				turnCtx,
				time.Since(queuedAt),
				attribute.String("foya.queue.kind", "user_turn"),
			)
		}
		_ = b.engine.RunInput(turnCtx, sessionID, input)

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
		turnCtx, runner.cancel = context.WithCancelCause(context.Background())
		b.broadcastQueueLocked(sessionID)
		b.turns.mu.Unlock()
		input = queueInput(next)
		queuedAt = next.CreatedAt
	}
}

func queueInput(item queue.Message) message.UserInput {
	return message.UserInput{
		Text:            item.Text,
		Command:         item.Command,
		Attachments:     append([]message.AttachmentRef(nil), item.Attachments...),
		BrowserElements: append([]message.BrowserElement(nil), item.BrowserElements...),
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
