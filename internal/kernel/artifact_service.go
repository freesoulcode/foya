package kernel

import (
	"context"
	"errors"
	"io"

	conversation "github.com/freesoulcode/foya/internal/conversation"
)

func (b *Service) PutImage(
	ctx context.Context,
	sessionID, name string,
	source io.Reader,
) (conversation.AttachmentRef, error) {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return conversation.AttachmentRef{}, conversation.ErrNotFound
	}
	b.mu.RLock()
	store := b.artifacts
	b.mu.RUnlock()
	if store == nil {
		return conversation.AttachmentRef{}, errors.New("artifact store is unavailable")
	}
	return store.PutImage(ctx, sessionID, name, source)
}

func (b *Service) ReadArtifact(
	ctx context.Context,
	sessionID, artifactID string,
) ([]byte, conversation.AttachmentRef, error) {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return nil, conversation.AttachmentRef{}, conversation.ErrNotFound
	}
	b.mu.RLock()
	store := b.artifacts
	b.mu.RUnlock()
	if store == nil {
		return nil, conversation.AttachmentRef{}, errors.New("artifact store is unavailable")
	}
	return store.Read(ctx, sessionID, artifactID)
}

func (b *Service) DeleteArtifact(ctx context.Context, sessionID, artifactID string) error {
	if _, ok := b.sessions.Get(sessionID); !ok {
		return conversation.ErrNotFound
	}
	b.mu.RLock()
	store := b.artifacts
	b.mu.RUnlock()
	if store == nil {
		return errors.New("artifact store is unavailable")
	}
	return store.Delete(ctx, sessionID, artifactID)
}
