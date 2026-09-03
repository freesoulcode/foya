package channel

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

type SessionStore struct {
	mu      sync.RWMutex
	path    string
	version int
	chats   map[string]string
}

type persistedSessions struct {
	Version int               `json:"version"`
	Chats   map[string]string `json:"chats"`
}

func OpenSessionStore(path string) (*SessionStore, error) {
	store := &SessionStore{
		path:    path,
		version: 1,
		chats:   make(map[string]string),
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, err
	}
	var persisted persistedSessions
	if err := json.Unmarshal(data, &persisted); err != nil {
		return nil, err
	}
	if persisted.Version != 1 {
		return nil, errors.New("unsupported Feishu session store version")
	}
	if persisted.Chats != nil {
		store.chats = persisted.Chats
	}
	return store, nil
}

func (s *SessionStore) Get(chatID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.chats[chatID]
}

func (s *SessionStore) Set(chatID, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, hadPrevious := s.chats[chatID]
	s.chats[chatID] = sessionID
	if err := s.persistLocked(); err != nil {
		if hadPrevious {
			s.chats[chatID] = previous
		} else {
			delete(s.chats, chatID)
		}
		return err
	}
	return nil
}

func (s *SessionStore) persistLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(persistedSessions{
		Version: s.version,
		Chats:   s.chats,
	}, "", "  ")
	if err != nil {
		return err
	}
	temp := s.path + ".tmp"
	if err := os.WriteFile(temp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(temp, s.path)
}
