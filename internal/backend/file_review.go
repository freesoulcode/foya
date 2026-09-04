package backend

import (
	"context"
	"errors"
	"fmt"

	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/session"
	"github.com/freesoulcode/foya/internal/state"
)

type FileReviewSummary struct {
	Files          []RewindFilePreview
	FileStateToken string
	ThroughSeq     event.Seq
}

func (b *Backend) PendingFileReview(
	ctx context.Context,
	sessionID string,
) (FileReviewSummary, error) {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return FileReviewSummary{}, session.ErrNotFound
	}
	review, err := b.log.PendingFileReview(ctx, sessionID)
	if err != nil {
		return FileReviewSummary{}, err
	}
	candidates, token, err := inspectFileRewind(ctx, b.log, review.Changes)
	if err != nil {
		return FileReviewSummary{}, err
	}
	return FileReviewSummary{
		Files:          b.displayRewindFiles(sessionID, candidates),
		FileStateToken: token,
		ThroughSeq:     review.ThroughSeq,
	}, nil
}

func (b *Backend) KeepFileChanges(
	ctx context.Context,
	sessionID string,
	expectedThroughSeq event.Seq,
) error {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return session.ErrNotFound
	}
	b.turns.mu.Lock()
	defer b.turns.mu.Unlock()
	if err := b.fileReviewSessionAvailableLocked(sessionID); err != nil {
		return err
	}

	review, err := b.log.PendingFileReview(ctx, sessionID)
	if err != nil {
		return err
	}
	results := uniqueFileReviewResults(review.Changes, event.RewindFileKept)
	resolved, err := b.log.ResolveFileReview(
		ctx,
		sessionID,
		expectedThroughSeq,
		event.FileReviewKept,
		results,
		"",
	)
	if err != nil {
		return mapFileReviewStateError(err)
	}
	_ = b.bus.PublishMustDeliver(
		context.WithoutCancel(ctx),
		"session:"+sessionID,
		resolved,
	)
	return nil
}

func (b *Backend) UndoFileChanges(
	ctx context.Context,
	sessionID string,
	expectedThroughSeq event.Seq,
	expectedFileState string,
	forceFileKeys []string,
) error {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return session.ErrNotFound
	}
	b.turns.mu.Lock()
	defer b.turns.mu.Unlock()
	if err := b.fileReviewSessionAvailableLocked(sessionID); err != nil {
		return err
	}

	review, err := b.log.PendingFileReview(ctx, sessionID)
	if err != nil {
		return err
	}
	if review.ThroughSeq != expectedThroughSeq {
		return ErrFileReviewChanged
	}
	candidates, token, err := inspectFileRewind(ctx, b.log, review.Changes)
	if err != nil {
		return err
	}
	if token != expectedFileState {
		return ErrFileStateChanged
	}
	results, plans, err := selectFileRewindPlans(candidates, forceFileKeys)
	if err != nil {
		return err
	}

	journalID := ""
	if len(plans) > 0 {
		journalID, err = b.log.BeginFileRewind(
			ctx,
			sessionID,
			0,
			expectedThroughSeq,
			fileRewindBackups(plans),
		)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrFileRewindFailed, err)
		}
	}
	restoreFiles, err := applyFileRewindPlans(plans)
	if err != nil {
		if journalID != "" {
			_ = b.log.FinishFileRewind(context.WithoutCancel(ctx), journalID)
		}
		if errors.Is(err, ErrFileRewindConflict) {
			return err
		}
		return fmt.Errorf("%w: %v", ErrFileRewindFailed, err)
	}
	resolved, err := b.log.ResolveFileReview(
		ctx,
		sessionID,
		expectedThroughSeq,
		event.FileReviewUndone,
		results,
		journalID,
	)
	if err != nil {
		restoreErr := restoreFiles()
		if journalID != "" {
			_ = b.log.FinishFileRewind(context.WithoutCancel(ctx), journalID)
		}
		if restoreErr != nil {
			return errors.Join(err, fmt.Errorf("restore files after failed review commit: %w", restoreErr))
		}
		return mapFileReviewStateError(err)
	}
	if journalID != "" {
		_ = b.log.FinishFileRewind(context.WithoutCancel(ctx), journalID)
	}
	b.engine.InvalidateHistoryEstimate(sessionID)
	_ = b.bus.PublishMustDeliver(
		context.WithoutCancel(ctx),
		"session:"+sessionID,
		resolved,
	)
	return nil
}

func (b *Backend) fileReviewSessionAvailableLocked(sessionID string) error {
	if _, deleted := b.turns.deleted[sessionID]; deleted {
		return session.ErrNotFound
	}
	if b.turns.runners[sessionID] != nil || b.turns.compacting[sessionID] {
		return ErrSessionBusy
	}
	if len(b.turns.queues[sessionID]) > 0 {
		return ErrSessionQueueNotEmpty
	}
	return nil
}

func uniqueFileReviewResults(
	changes []state.RewindFileChange,
	action string,
) []event.RewindFileResult {
	results := make([]event.RewindFileResult, 0)
	seen := make(map[string]struct{})
	for _, item := range changes {
		path := item.Change.Path
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		results = append(results, event.RewindFileResult{
			Path:   path,
			Action: action,
		})
	}
	return results
}

func mapFileReviewStateError(err error) error {
	switch {
	case errors.Is(err, state.ErrNoPendingFileChanges):
		return ErrNoPendingFileChanges
	case errors.Is(err, state.ErrFileReviewChanged):
		return ErrFileReviewChanged
	default:
		return err
	}
}
