// Package queue defines messages waiting for a session's active turn to finish.
package conversation

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// QueuedMessage is a user message waiting for a session's active turn.
type QueuedMessage struct {
	ID              string           `json:"id"`
	SessionID       string           `json:"session_id"`
	Text            string           `json:"text"`
	Command         string           `json:"command,omitempty"`
	SkillRef        string           `json:"skill_ref,omitempty"`
	Attachments     []AttachmentRef  `json:"attachments,omitempty"`
	BrowserElements []BrowserElement `json:"browser_elements,omitempty"`
	Position        int              `json:"position"`
	CreatedAt       time.Time        `json:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at"`
}

// QueueSnapshot is the complete ordered queue projected to clients.
type QueueSnapshot struct {
	Items []QueuedMessage `json:"items"`
}

// NewQueuedMessage creates a queued message with a stable client-facing ID.
func NewQueuedMessage(sessionID string, input UserInput, position int) QueuedMessage {
	now := time.Now()
	return QueuedMessage{
		ID:              newQueueID(),
		SessionID:       sessionID,
		Text:            input.Text,
		Command:         input.Command,
		SkillRef:        input.SkillRef,
		Attachments:     append([]AttachmentRef(nil), input.Attachments...),
		BrowserElements: append([]BrowserElement(nil), input.BrowserElements...),
		Position:        position,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

func newQueueID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
