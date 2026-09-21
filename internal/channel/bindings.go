package channel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/freesoulcode/foya/internal/storage"
)

var (
	ErrConversationNotFound = errors.New("channel conversation not found")
	ErrConversationUnbound  = errors.New("channel conversation is not bound")
)

type ConversationBinding struct {
	ChannelID       string    `json:"channel_id"`
	ConversationKey string    `json:"conversation_key"`
	Kind            string    `json:"kind"`
	ExternalID      string    `json:"external_id"`
	DisplayName     string    `json:"display_name,omitempty"`
	ActiveSessionID string    `json:"active_session_id,omitempty"`
	LastSeenAt      time.Time `json:"last_seen_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type ConversationBindingStore struct {
	db *storage.Database
}

func NewConversationBindingStore(db *storage.Database) *ConversationBindingStore {
	return &ConversationBindingStore{db: db}
}

func (s *ConversationBindingStore) Observe(
	ctx context.Context,
	channelID, conversationKey, kind, externalID, displayName string,
) error {
	channelID = strings.TrimSpace(channelID)
	conversationKey = strings.TrimSpace(conversationKey)
	kind = strings.TrimSpace(kind)
	externalID = strings.TrimSpace(externalID)
	displayName = strings.TrimSpace(displayName)
	if channelID == "" || conversationKey == "" || kind == "" || externalID == "" {
		return errors.New("channel ID, conversation key, kind, and external ID are required")
	}
	now := time.Now().UnixNano()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO channel_conversations(
			channel_id, conversation_key, kind, external_id, display_name,
			active_session_id, last_seen_at_ns, updated_at_ns
		) VALUES (?, ?, ?, ?, ?, '', ?, ?)
		ON CONFLICT(channel_id, conversation_key) DO UPDATE SET
			kind = excluded.kind,
			external_id = excluded.external_id,
			display_name = CASE
				WHEN excluded.display_name = '' THEN channel_conversations.display_name
				ELSE excluded.display_name
			END,
			last_seen_at_ns = excluded.last_seen_at_ns
	`, channelID, conversationKey, kind, externalID, displayName, now, now)
	if err != nil {
		return fmt.Errorf("observe channel conversation: %w", err)
	}
	return nil
}

func (s *ConversationBindingStore) Get(
	ctx context.Context,
	channelID, conversationKey string,
) (string, error) {
	var sessionID string
	err := s.db.QueryRowContext(ctx, `
		SELECT active_session_id
		FROM channel_conversations
		WHERE channel_id = ? AND conversation_key = ?
	`, strings.TrimSpace(channelID), strings.TrimSpace(conversationKey)).Scan(&sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read channel conversation: %w", err)
	}
	return sessionID, nil
}

func (s *ConversationBindingStore) List(
	ctx context.Context,
	channelID string,
) ([]ConversationBinding, error) {
	channelID = strings.TrimSpace(channelID)
	query := `
		SELECT channel_id, conversation_key, kind, external_id, display_name,
		       active_session_id, last_seen_at_ns, updated_at_ns
		FROM channel_conversations
	`
	args := make([]any, 0, 1)
	if channelID != "" {
		query += " WHERE channel_id = ?"
		args = append(args, channelID)
	}
	query += " ORDER BY last_seen_at_ns DESC, channel_id, conversation_key"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list channel conversations: %w", err)
	}
	defer rows.Close()
	items := make([]ConversationBinding, 0)
	for rows.Next() {
		item, err := scanConversationBinding(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate channel conversations: %w", err)
	}
	return items, nil
}

func (s *ConversationBindingStore) ForSession(
	ctx context.Context,
	sessionID string,
) (ConversationBinding, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT channel_id, conversation_key, kind, external_id, display_name,
		       active_session_id, last_seen_at_ns, updated_at_ns
		FROM channel_conversations
		WHERE active_session_id = ?
	`, strings.TrimSpace(sessionID))
	item, err := scanConversationBinding(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ConversationBinding{}, nil
	}
	return item, err
}

func (s *ConversationBindingStore) Bind(
	ctx context.Context,
	channelID, conversationKey, sessionID string,
) error {
	channelID = strings.TrimSpace(channelID)
	conversationKey = strings.TrimSpace(conversationKey)
	sessionID = strings.TrimSpace(sessionID)
	if channelID == "" || conversationKey == "" || sessionID == "" {
		return errors.New("channel ID, conversation key, and session ID are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin channel conversation update: %w", err)
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRowContext(
		ctx,
		`SELECT 1 FROM channel_conversations WHERE channel_id = ? AND conversation_key = ?`,
		channelID,
		conversationKey,
	).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return ErrConversationNotFound
	} else if err != nil {
		return fmt.Errorf("resolve channel conversation: %w", err)
	}
	now := time.Now().UnixNano()
	if _, err := tx.ExecContext(ctx, `
		UPDATE channel_conversations
		SET active_session_id = '', updated_at_ns = ?
		WHERE active_session_id = ?
		  AND (channel_id != ? OR conversation_key != ?)
	`, now, sessionID, channelID, conversationKey); err != nil {
		return fmt.Errorf("release previous session channel binding: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE channel_conversations
		SET active_session_id = ?, updated_at_ns = ?
		WHERE channel_id = ? AND conversation_key = ?
	`, sessionID, now, channelID, conversationKey)
	if err != nil {
		return fmt.Errorf("bind channel conversation: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read channel binding count: %w", err)
	}
	if affected == 0 {
		return ErrConversationNotFound
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit channel conversation update: %w", err)
	}
	return nil
}

func (s *ConversationBindingStore) UnbindSession(
	ctx context.Context,
	sessionID string,
) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE channel_conversations
		SET active_session_id = '', updated_at_ns = ?
		WHERE active_session_id = ?
	`, time.Now().UnixNano(), strings.TrimSpace(sessionID))
	if err != nil {
		return fmt.Errorf("unbind session from channel conversation: %w", err)
	}
	return nil
}

func (s *ConversationBindingStore) DeleteChannel(ctx context.Context, channelID string) error {
	channelID = strings.TrimSpace(channelID)
	if _, err := s.db.ExecContext(
		ctx,
		`DELETE FROM channel_conversations WHERE channel_id = ?`,
		channelID,
	); err != nil {
		return fmt.Errorf("delete channel conversations: %w", err)
	}
	return nil
}

type bindingScanner interface {
	Scan(...any) error
}

func scanConversationBinding(scanner bindingScanner) (ConversationBinding, error) {
	var (
		item                  ConversationBinding
		lastSeenAt, updatedAt int64
	)
	if err := scanner.Scan(
		&item.ChannelID,
		&item.ConversationKey,
		&item.Kind,
		&item.ExternalID,
		&item.DisplayName,
		&item.ActiveSessionID,
		&lastSeenAt,
		&updatedAt,
	); err != nil {
		return ConversationBinding{}, err
	}
	item.LastSeenAt = time.Unix(0, lastSeenAt)
	item.UpdatedAt = time.Unix(0, updatedAt)
	return item, nil
}
