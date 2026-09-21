package channel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/freesoulcode/foya/internal/conversation"
)

const defaultPairingTTL = 10 * time.Minute

var (
	ErrPairingNotFound        = errors.New("channel pairing not found")
	ErrPairingExpired         = errors.New("channel pairing expired")
	ErrPairingAlreadyUsed     = errors.New("channel pairing already used")
	ErrPairingChannelMismatch = errors.New("channel pairing belongs to another channel")
	ErrPairingSessionNotFound = errors.New("channel pairing session not found")
)

type PairingStatus string

const (
	PairingPending   PairingStatus = "pending"
	PairingCompleted PairingStatus = "completed"
	PairingExpired   PairingStatus = "expired"
	PairingCancelled PairingStatus = "cancelled"
)

type ConversationPairing struct {
	ID           string               `json:"id"`
	Code         string               `json:"code"`
	Command      string               `json:"command"`
	ChannelID    string               `json:"channel_id"`
	SessionID    string               `json:"session_id"`
	Status       PairingStatus        `json:"status"`
	Conversation *ConversationBinding `json:"conversation,omitempty"`
	CreatedAt    time.Time            `json:"created_at"`
	ExpiresAt    time.Time            `json:"expires_at"`
	CompletedAt  *time.Time           `json:"completed_at,omitempty"`
}

type ConversationPairingStore struct {
	mu        sync.Mutex
	runtime   Runtime
	bindings  *ConversationBindingStore
	ttl       time.Duration
	byID      map[string]*ConversationPairing
	byCode    map[string]*ConversationPairing
	bySession map[string]*ConversationPairing
}

func NewConversationPairingStore(
	bindings *ConversationBindingStore,
	runtime Runtime,
) *ConversationPairingStore {
	return &ConversationPairingStore{
		runtime:   runtime,
		bindings:  bindings,
		ttl:       defaultPairingTTL,
		byID:      make(map[string]*ConversationPairing),
		byCode:    make(map[string]*ConversationPairing),
		bySession: make(map[string]*ConversationPairing),
	}
}

func ParsePairingCommand(content string) (string, bool) {
	fields := strings.Fields(strings.TrimSpace(content))
	if len(fields) == 0 {
		return "", false
	}
	command, _, _ := strings.Cut(fields[0], "@")
	if !strings.EqualFold(command, "/bind") {
		return "", false
	}
	if len(fields) != 2 {
		return "", true
	}
	return strings.ToUpper(strings.TrimSpace(fields[1])), true
}

func (s *ConversationPairingStore) Create(
	channelID, sessionID string,
) (ConversationPairing, error) {
	channelID = strings.TrimSpace(channelID)
	sessionID = strings.TrimSpace(sessionID)
	if channelID == "" || sessionID == "" {
		return ConversationPairing{}, errors.New("channel ID and session ID are required")
	}
	if !sessionExists(s.runtime.ListSessions(), sessionID) {
		return ConversationPairing{}, ErrPairingSessionNotFound
	}
	id, err := randomHex(16)
	if err != nil {
		return ConversationPairing{}, err
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked(now)
	code, err := s.uniqueCodeLocked()
	if err != nil {
		return ConversationPairing{}, err
	}
	item := &ConversationPairing{
		ID:        id,
		Code:      code,
		Command:   "/bind " + code,
		ChannelID: channelID,
		SessionID: sessionID,
		Status:    PairingPending,
		CreatedAt: now,
		ExpiresAt: now.Add(s.ttl),
	}
	if previous := s.bySession[sessionID]; previous != nil &&
		previous.Status == PairingPending {
		previous.Status = PairingCancelled
		delete(s.byCode, previous.Code)
	}
	s.byID[id] = item
	s.byCode[code] = item
	s.bySession[sessionID] = item
	return clonePairing(item), nil
}

func (s *ConversationPairingStore) Get(id string) (ConversationPairing, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked(time.Now())
	item := s.byID[strings.TrimSpace(id)]
	if item == nil {
		return ConversationPairing{}, false
	}
	return clonePairing(item), true
}

func (s *ConversationPairingStore) Cancel(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked(time.Now())
	item := s.byID[strings.TrimSpace(id)]
	if item == nil {
		return ErrPairingNotFound
	}
	if item.Status == PairingPending {
		item.Status = PairingCancelled
		delete(s.byCode, item.Code)
	}
	return nil
}

func (s *ConversationPairingStore) Consume(
	ctx context.Context,
	channelID, code, conversationKey, kind, externalID, displayName string,
) (ConversationPairing, error) {
	channelID = strings.TrimSpace(channelID)
	code = strings.ToUpper(strings.TrimSpace(code))
	if channelID == "" || code == "" {
		return ConversationPairing{}, ErrPairingNotFound
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked(time.Now())
	item := s.byCode[code]
	if item == nil {
		return ConversationPairing{}, ErrPairingNotFound
	}
	if item.ChannelID != channelID {
		return ConversationPairing{}, ErrPairingChannelMismatch
	}
	if item.Status == PairingExpired {
		return ConversationPairing{}, ErrPairingExpired
	}
	if item.Status == PairingCancelled {
		return ConversationPairing{}, ErrPairingNotFound
	}
	if item.Status == PairingCompleted {
		if item.Conversation != nil &&
			item.Conversation.ConversationKey == conversationKey {
			return clonePairing(item), nil
		}
		return ConversationPairing{}, ErrPairingAlreadyUsed
	}
	if !sessionExists(s.runtime.ListSessions(), item.SessionID) {
		item.Status = PairingCancelled
		delete(s.byCode, item.Code)
		return ConversationPairing{}, ErrPairingSessionNotFound
	}
	if err := s.bindings.Observe(
		ctx,
		channelID,
		conversationKey,
		kind,
		externalID,
		displayName,
	); err != nil {
		return ConversationPairing{}, err
	}
	if err := s.bindings.Bind(
		ctx,
		channelID,
		conversationKey,
		item.SessionID,
	); err != nil {
		return ConversationPairing{}, err
	}
	binding, err := s.bindings.ForSession(ctx, item.SessionID)
	if err != nil {
		return ConversationPairing{}, err
	}
	now := time.Now()
	item.Status = PairingCompleted
	item.Conversation = &binding
	item.CompletedAt = &now
	return clonePairing(item), nil
}

func (s *ConversationPairingStore) uniqueCodeLocked() (string, error) {
	const alphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"
	for range 8 {
		raw := make([]byte, 8)
		if _, err := rand.Read(raw); err != nil {
			return "", err
		}
		for index := range raw {
			raw[index] = alphabet[int(raw[index])%len(alphabet)]
		}
		code := string(raw)
		_, exists := s.byCode[code]
		if !exists {
			return code, nil
		}
	}
	return "", errors.New("generate unique channel pairing code")
}

func sessionExists(items []*conversation.Session, sessionID string) bool {
	for _, item := range items {
		if item.ID == sessionID {
			return true
		}
	}
	return false
}

func (s *ConversationPairingStore) expireLocked(now time.Time) {
	for _, item := range s.byCode {
		if item.Status == PairingPending && !now.Before(item.ExpiresAt) {
			item.Status = PairingExpired
		}
	}
	for id, item := range s.byID {
		if now.Sub(item.ExpiresAt) > time.Hour {
			delete(s.byID, id)
			delete(s.byCode, item.Code)
			if s.bySession[item.SessionID] == item {
				delete(s.bySession, item.SessionID)
			}
		}
	}
}

func clonePairing(item *ConversationPairing) ConversationPairing {
	cloned := *item
	if item.Conversation != nil {
		conversation := *item.Conversation
		cloned.Conversation = &conversation
	}
	return cloned
}

func randomHex(size int) (string, error) {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}
