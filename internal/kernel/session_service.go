package kernel

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/freesoulcode/foya/internal/artifact"
	"github.com/freesoulcode/foya/internal/config"
	conversation "github.com/freesoulcode/foya/internal/conversation"
	interaction "github.com/freesoulcode/foya/internal/interaction"

	"github.com/freesoulcode/foya/internal/project"

	"github.com/freesoulcode/foya/internal/terminal"
	"github.com/freesoulcode/foya/internal/tool"
)

// CreateSession creates a chat.
func (b *Service) CreateSession(opts conversation.CreateOptions) (*conversation.Session, error) {
	b.projectMu.Lock()
	defer b.projectMu.Unlock()
	if opts.ConnectionID == "" && opts.Model == "" {
		if defaults, err := b.DefaultModels(); err == nil && defaults.Language.ConnectionID != "" {
			opts.ConnectionID = defaults.Language.ConnectionID
			opts.Model = defaults.Language.Model
		}
	}
	b.mu.RLock()
	connectionID := opts.ConnectionID
	if connectionID == "" {
		connectionID = b.firstConnectionID
	}
	connection, exists := b.connections[connectionID]
	b.mu.RUnlock()
	if connectionID != "" && !exists {
		return nil, fmt.Errorf("%w: %q", ErrConnectionNotFound, connectionID)
	}
	if connectionID != "" && connection.Type != config.ConnectionTypeLanguage {
		return nil, errors.New("sessions require a language model connection")
	}
	opts.ConnectionID = connectionID
	if err := b.resolveSessionProject(&opts); err != nil {
		return nil, err
	}
	created, err := b.sessions.Create(opts)
	if err != nil {
		return nil, err
	}

	b.engine.RunSessionStart(context.Background(), created.ID)
	if created.ParentID == "" {
		b.mu.RLock()
		wake := b.memoryWake
		b.mu.RUnlock()
		if wake != nil {
			wake()
		}
	}
	return created, nil
}

// ForkSession copies the current active message history into a new top-level
// session. Runtime state such as queues, approvals and in-flight turns is not
// copied.
func (b *Service) ForkSession(
	ctx context.Context,
	sourceID string,
	opts ForkSessionOptions,
) (*conversation.Session, error) {
	b.turns.mu.Lock()
	defer b.turns.mu.Unlock()

	source, ok := b.sessions.Get(sourceID)
	if !ok {
		return nil, conversation.ErrNotFound
	}
	if _, deleted := b.turns.deleted[sourceID]; deleted {
		return nil, conversation.ErrNotFound
	}
	if b.turns.runners[sourceID] != nil ||
		b.turns.compacting[sourceID] ||
		len(b.turns.queues[sourceID]) > 0 {
		return nil, ErrSessionBusy
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	history, err := b.log.History(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	history, err = forkHistoryThrough(history, opts.ThroughSeq)
	if err != nil {
		return nil, err
	}
	throughSeq := opts.ThroughSeq
	if throughSeq == 0 {
		throughSeq = forkThroughSeq(history)
	}
	title := strings.TrimSpace(opts.Title)
	if title == "" {
		title = defaultForkTitle(source.Title)
	}
	forked, err := b.sessions.Create(conversation.CreateOptions{
		ConnectionID:    source.ConnectionID,
		Model:           source.Model,
		ReasoningEffort: source.ReasoningEffort,
		ProjectID:       source.ProjectID,
		ApprovalMode:    source.ApprovalMode,
		Title:           title,
		TitleIsManual:   true,
	})
	if err != nil {
		return nil, err
	}

	copiedHistory, artifactStore, err := b.copyForkHistoryArtifacts(
		ctx,
		sourceID,
		forked.ID,
		history,
	)
	if err != nil {
		b.cleanupFailedFork(ctx, forked.ID, artifactStore)
		return nil, err
	}
	if _, err := b.log.ImportMessages(
		ctx,
		forked.ID,
		sourceID,
		throughSeq,
		copiedHistory,
	); err != nil {
		b.cleanupFailedFork(ctx, forked.ID, artifactStore)
		return nil, err
	}

	return forked, nil
}

func (b *Service) cleanupFailedFork(
	ctx context.Context,
	sessionID string,
	store artifact.Store,
) {
	_ = b.sessions.Delete(sessionID)
	if store != nil {
		_ = store.DeleteSession(ctx, sessionID)
	}
}

func (b *Service) copyForkHistoryArtifacts(
	ctx context.Context,
	sourceID string,
	targetID string,
	history []conversation.Message,
) ([]conversation.Message, artifact.Store, error) {
	out := make([]conversation.Message, len(history))
	copy(out, history)

	var needsArtifacts bool
	for _, item := range out {
		if len(item.Attachments) > 0 {
			needsArtifacts = true
			break
		}
	}
	if !needsArtifacts {
		return out, nil, nil
	}

	b.mu.RLock()
	store := b.artifacts
	b.mu.RUnlock()
	if store == nil {
		return nil, nil, errors.New("artifact store is unavailable")
	}

	copied := make(map[string]conversation.AttachmentRef)
	commitIDs := make([]string, 0)
	for i := range out {
		refs, err := copyAttachmentRefs(
			ctx,
			store,
			sourceID,
			targetID,
			out[i].Attachments,
			copied,
			&commitIDs,
		)
		if err != nil {
			return nil, store, err
		}
		out[i].Attachments = refs
	}
	if len(commitIDs) > 0 {
		if err := store.Commit(ctx, targetID, commitIDs); err != nil {
			return nil, store, fmt.Errorf("commit forked artifacts: %w", err)
		}
	}
	return out, store, nil
}

func copyAttachmentRefs(
	ctx context.Context,
	store artifact.Store,
	sourceID string,
	targetID string,
	refs []conversation.AttachmentRef,
	copied map[string]conversation.AttachmentRef,
	commitIDs *[]string,
) ([]conversation.AttachmentRef, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	out := make([]conversation.AttachmentRef, 0, len(refs))
	for _, ref := range refs {
		if existing, ok := copied[ref.ID]; ok {
			out = append(out, existing)
			continue
		}
		data, canonical, err := store.Read(ctx, sourceID, ref.ID)
		if err != nil {
			return nil, fmt.Errorf("read fork source artifact %q: %w", ref.ID, err)
		}
		if canonical.Kind != "image" {
			return nil, fmt.Errorf("unsupported fork artifact kind %q", canonical.Kind)
		}
		next, err := store.PutImage(ctx, targetID, canonical.Name, bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("write fork artifact %q: %w", ref.ID, err)
		}
		copied[ref.ID] = next
		*commitIDs = append(*commitIDs, next.ID)
		out = append(out, next)
	}
	return out, nil
}

func forkThroughSeq(history []conversation.Message) conversation.Seq {
	var through conversation.Seq
	for _, item := range history {
		if conversation.Seq(item.EventSeq) > through {
			through = conversation.Seq(item.EventSeq)
		}
	}
	return through
}

func forkHistoryThrough(
	history []conversation.Message,
	throughSeq conversation.Seq,
) ([]conversation.Message, error) {
	if throughSeq == 0 {
		return history, nil
	}
	for i, item := range history {
		if conversation.Seq(item.EventSeq) == throughSeq {
			if item.Role == conversation.RoleUser {
				return nil, ErrInvalidForkBoundary
			}
			return history[:i+1], nil
		}
	}
	return nil, ErrActiveMessageNotFound
}

func defaultForkTitle(sourceTitle string) string {
	base := strings.TrimSpace(sourceTitle)
	if base == "" {
		base = "New chat"
	}
	return base + " copy"
}

// UpdateSession partially updates mutable chat settings.
func (b *Service) UpdateSession(
	ctx context.Context,
	id string,
	connectionID, model, reasoningEffort, projectID, approvalMode *string,
) (*conversation.Session, error) {
	b.projectMu.Lock()
	defer b.projectMu.Unlock()
	if connectionID != nil {
		b.mu.RLock()
		_, exists := b.connections[*connectionID]
		b.mu.RUnlock()
		if !exists {
			return nil, fmt.Errorf("%w: %q", ErrConnectionNotFound, *connectionID)
		}
	}
	if projectID != nil && *projectID != "" {
		if _, err := b.Project(*projectID); err != nil {
			return nil, err
		}
	}
	updated, err := b.sessions.Update(id, connectionID, model, reasoningEffort, projectID, approvalMode)
	if err != nil {
		return nil, err
	}
	b.broadcastSession(ctx, updated)
	return updated, nil
}

func (b *Service) resolveSessionProject(opts *conversation.CreateOptions) error {
	b.mu.RLock()
	manager := b.projects
	b.mu.RUnlock()
	if manager == nil {
		return nil
	}
	if opts.ProjectID != "" {
		_, ok := manager.Get(opts.ProjectID)
		if !ok {
			return project.ErrNotFound
		}
	}
	return nil
}

// RenameSession assigns a manual chat title.
func (b *Service) RenameSession(ctx context.Context, id, title string) (*conversation.Session, error) {
	if err := b.sessions.Rename(id, title); err != nil {
		return nil, err
	}
	s, ok := b.sessions.Get(id)
	if !ok {
		return nil, conversation.ErrNotFound
	}
	b.broadcastSession(ctx, s)
	return s, nil
}

// PinSession updates pin state and broadcasts session_updated.
func (b *Service) PinSession(ctx context.Context, id string, pinned bool) (*conversation.Session, error) {
	s, err := b.sessions.SetPinned(id, pinned)
	if err != nil {
		return nil, err
	}
	b.broadcastSession(ctx, s)
	return s, nil
}

// deleteTurnGrace bounds shutdown of an active turn during deletion.
const deleteTurnGrace = 3 * time.Second

// DeleteSession cancels active work, removes metadata and events, then
// broadcasts session_deleted to connected clients.
//
// session_deleted is broadcast but not persisted in the removed chat partition.
func (b *Service) DeleteSession(ctx context.Context, id string) error {
	if _, ok := b.sessions.Get(id); !ok {
		return conversation.ErrNotFound
	}
	all := b.sessions.List()
	var ordered []string
	var visit func(string)
	visit = func(parentID string) {
		for _, item := range all {
			if item.ParentID == parentID {
				visit(item.ID)
			}
		}
		ordered = append(ordered, parentID)
	}
	visit(id)
	for _, sessionID := range ordered {
		b.stopSessionAndWait(sessionID, deleteTurnGrace)
		b.terminal.CloseSession(sessionID)
		b.approval.ClearSession(sessionID)
		b.mu.RLock()
		questions := b.questions
		b.mu.RUnlock()
		if questions != nil {
			questions.ClearSession(sessionID)
		}
		b.mu.RLock()
		backgroundCommands := b.backgroundCommands
		b.mu.RUnlock()
		if backgroundCommands != nil {
			backgroundCommands.ClearSession(sessionID)
		}
		b.mu.RLock()
		browserController := b.browser
		b.mu.RUnlock()
		if browserController != nil {
			browserController.ClearSession(sessionID)
		}
		b.engine.RunSessionEnd(context.WithoutCancel(ctx), sessionID, "deleted")
		if err := b.sessions.Delete(sessionID); err != nil {
			return err
		}
		if err := b.log.Delete(ctx, sessionID); err != nil {
			return err
		}
		b.mu.RLock()
		artifactStore := b.artifacts
		b.mu.RUnlock()
		if artifactStore != nil {
			_ = artifactStore.DeleteSession(ctx, sessionID)
		}
		ev := conversation.Event{
			Kind:    conversation.KindSessionDeleted,
			Session: sessionID,
			Time:    time.Now(),
			Payload: map[string]string{"id": sessionID},
		}
		_ = b.bus.PublishMustDeliver(ctx, "session:"+sessionID, ev)
	}
	return nil
}

// broadcastSession persists and publishes the current chat state.
func (b *Service) broadcastSession(ctx context.Context, s *conversation.Session) {
	ev := conversation.Event{Kind: conversation.KindSessionUpdated, Session: s.ID, Time: time.Now(), Payload: s}
	seq, _ := b.log.Append(ctx, ev)
	ev.Seq = seq
	_ = b.bus.PublishMustDeliver(ctx, "session:"+s.ID, ev)
}

// ListSessions lists chats.
func (b *Service) ListSessions() []*conversation.Session {
	all := b.sessions.List()
	out := make([]*conversation.Session, 0, len(all))
	for _, item := range all {
		if item.ParentID == "" {
			out = append(out, item)
		}
	}
	return out
}

// ChildSessions returns direct children of one parent session.
func (b *Service) ChildSessions(parentID string) ([]*conversation.Session, error) {
	if _, ok := b.sessions.Get(parentID); !ok {
		return nil, conversation.ErrNotFound
	}
	all := b.sessions.List()
	out := make([]*conversation.Session, 0)
	for _, item := range all {
		if item.ParentID == parentID {
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

// CancelTurn stops active work while preserving and pausing queued messages.
func (b *Service) CancelTurn(sessionID string) {
	b.cancelCurrentTurn(sessionID)
	b.mu.RLock()
	manager := b.subagents
	b.mu.RUnlock()
	if manager != nil {
		manager.CancelTree(sessionID)
	}
}

func (b *Service) CancelTool(sessionID, toolCallID string) error {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return conversation.ErrNotFound
	}
	if !b.engine.CancelTool(sessionID, toolCallID) {
		return ErrToolCallNotRunning
	}
	return nil
}

func (b *Service) BackgroundTool(
	sessionID, toolCallID string,
) (tool.BackgroundCommandSnapshot, error) {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return tool.BackgroundCommandSnapshot{}, conversation.ErrNotFound
	}
	b.mu.RLock()
	manager := b.backgroundCommands
	b.mu.RUnlock()
	if manager == nil {
		return tool.BackgroundCommandSnapshot{}, errors.New("background commands are unavailable")
	}
	return manager.Promote(sessionID, toolCallID)
}

func (b *Service) RevealToolCommand(
	sessionID, toolCallID string,
) (tool.BackgroundCommandSnapshot, error) {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return tool.BackgroundCommandSnapshot{}, conversation.ErrNotFound
	}
	b.mu.RLock()
	manager := b.backgroundCommands
	b.mu.RUnlock()
	if manager == nil {
		return tool.BackgroundCommandSnapshot{}, errors.New("managed commands are unavailable")
	}
	return manager.Reveal(sessionID, toolCallID)
}

func (b *Service) ListBackgroundCommands(
	sessionID string,
) ([]tool.BackgroundCommandSnapshot, error) {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return nil, conversation.ErrNotFound
	}
	b.mu.RLock()
	manager := b.backgroundCommands
	b.mu.RUnlock()
	if manager == nil {
		return nil, errors.New("background commands are unavailable")
	}
	return manager.List(sessionID), nil
}

func (b *Service) GetBackgroundCommand(
	sessionID, commandID string,
) (tool.BackgroundCommandSnapshot, error) {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return tool.BackgroundCommandSnapshot{}, conversation.ErrNotFound
	}
	b.mu.RLock()
	manager := b.backgroundCommands
	b.mu.RUnlock()
	if manager == nil {
		return tool.BackgroundCommandSnapshot{}, errors.New("background commands are unavailable")
	}
	return manager.Get(sessionID, commandID)
}

func (b *Service) StopBackgroundCommand(
	sessionID, commandID string,
) (tool.BackgroundCommandSnapshot, error) {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return tool.BackgroundCommandSnapshot{}, conversation.ErrNotFound
	}
	b.mu.RLock()
	manager := b.backgroundCommands
	b.mu.RUnlock()
	if manager == nil {
		return tool.BackgroundCommandSnapshot{}, errors.New("background commands are unavailable")
	}
	return manager.Stop(sessionID, commandID, "user")
}

// ResolveApproval applies a decision received through REST.
func (b *Service) ResolveApproval(requestID string, decision string) error {
	d := interaction.Decision(decision)
	return b.approval.Resolve(requestID, d)
}

// AnswerQuestions supplies the complete response set for one pending ask_user
// request. The gateway's take semantics make this safe across clients.
func (b *Service) AnswerQuestions(sessionID, batchID string, answers []interaction.Answer) error {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return conversation.ErrNotFound
	}
	b.mu.RLock()
	gateway := b.questions
	b.mu.RUnlock()
	if gateway == nil {
		return errors.New("question gateway is unavailable")
	}
	return gateway.Answer(sessionID, batchID, answers)
}

// CancelQuestions abandons one pending ask_user request without cancelling the
// whole conversation.
func (b *Service) CancelQuestions(sessionID, batchID string) error {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return conversation.ErrNotFound
	}
	b.mu.RLock()
	gateway := b.questions
	b.mu.RUnlock()
	if gateway == nil {
		return errors.New("question gateway is unavailable")
	}
	return gateway.Cancel(sessionID, batchID)
}

// StartTerminal starts an interactive shell in the session project.
func (b *Service) StartTerminal(
	ctx context.Context,
	sessionID string,
	cols, rows uint16,
) (terminal.Snapshot, error) {
	s, ok := b.sessions.Get(sessionID)
	if !ok {
		return terminal.Snapshot{}, conversation.ErrNotFound
	}
	projectPath := ""
	if s.ProjectID != "" {
		item, err := b.Project(s.ProjectID)
		if err != nil {
			return terminal.Snapshot{}, err
		}
		projectPath = item.Path
	}
	return b.terminal.Start(ctx, sessionID, projectPath, cols, rows)
}

// AttachTerminal returns the current recoverable terminal snapshot.
func (b *Service) AttachTerminal(sessionID, ref string) (terminal.Snapshot, error) {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return terminal.Snapshot{}, conversation.ErrNotFound
	}
	return b.terminal.Attach(sessionID, ref)
}

// WriteTerminal forwards user input to an interactive shell.
func (b *Service) WriteTerminal(sessionID, ref, input string) error {
	return b.terminal.Write(sessionID, ref, input)
}

// ResizeTerminal updates the PTY geometry.
func (b *Service) ResizeTerminal(sessionID, ref string, cols, rows uint16) error {
	return b.terminal.Resize(sessionID, ref, cols, rows)
}

// StopTerminal terminates one interactive shell.
func (b *Service) StopTerminal(sessionID, ref string) error {
	return b.terminal.Stop(sessionID, ref)
}

// SubscribeTerminal streams ordered PTY output independently from chat events.
func (b *Service) SubscribeTerminal(
	ctx context.Context,
	sessionID, ref string,
	after uint64,
) (<-chan terminal.DataEvent, error) {
	return b.terminal.Subscribe(ctx, sessionID, ref, after)
}

// Subscribe returns a chat event stream.
func (b *Service) Subscribe(ctx context.Context, sessionID string) <-chan conversation.Event {
	return b.bus.Subscribe(ctx, "session:"+sessionID)
}

// History returns projected chat history.
func (b *Service) History(ctx context.Context, sessionID string) ([]conversation.Message, error) {
	return b.log.History(ctx, sessionID)
}

// Replay returns chat events with sequence numbers greater than after.
func (b *Service) Replay(ctx context.Context, sessionID string, after conversation.Seq) ([]conversation.Event, error) {
	return b.log.Read(ctx, sessionID, after)
}
