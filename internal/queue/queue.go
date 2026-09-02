// Package queue defines messages waiting for a session's active turn to finish.
package queue

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/freesoulcode/foya/internal/message"
)

// Message is a user message waiting to start a turn.
type Message struct {
	ID              string                   `json:"id"`
	SessionID       string                   `json:"session_id"`
	Text            string                   `json:"text"`
	Command         string                   `json:"command,omitempty"`
	Attachments     []message.AttachmentRef  `json:"attachments,omitempty"`
	BrowserElements []message.BrowserElement `json:"browser_elements,omitempty"`
	Position        int                      `json:"position"`
	CreatedAt       time.Time                `json:"created_at"`
	UpdatedAt       time.Time                `json:"updated_at"`
}

// Snapshot is the complete ordered queue projected to clients.
type Snapshot struct {
	Items []Message `json:"items"`
}

// NewMessage creates a queued message with a stable client-facing ID.
func NewMessage(sessionID string, input message.UserInput, position int) Message {
	now := time.Now()
	return Message{
		ID:              newID(),
		SessionID:       sessionID,
		Text:            input.Text,
		Command:         input.Command,
		Attachments:     append([]message.AttachmentRef(nil), input.Attachments...),
		BrowserElements: append([]message.BrowserElement(nil), input.BrowserElements...),
		Position:        position,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
