// Package terminal manages interactive shell resources owned by the kernel.
package terminal

import (
	"context"
	"errors"
)

var (
	ErrNotFound    = errors.New("terminal not found")
	ErrUnavailable = errors.New("interactive terminal is unavailable")
)

// Snapshot is the current recoverable state of one terminal resource.
type Snapshot struct {
	Ref       string `json:"ref"`
	SessionID string `json:"session_id"`
	Running   bool   `json:"running"`
	ExitCode  int    `json:"exit_code,omitempty"`
	Buffer    string `json:"buffer,omitempty"`
	Seq       uint64 `json:"seq"`
}

// DataEvent is one ordered terminal output or lifecycle update.
type DataEvent struct {
	SessionID string `json:"session_id"`
	Ref       string `json:"ref"`
	Seq       uint64 `json:"seq"`
	Data      string `json:"data,omitempty"`
	Exited    bool   `json:"exited,omitempty"`
	ExitCode  int    `json:"exit_code,omitempty"`
}

// Manager owns terminal processes independently from connected clients.
type Manager interface {
	Start(ctx context.Context, sessionID, workspace string, cols, rows uint16) (Snapshot, error)
	Attach(sessionID, ref string) (Snapshot, error)
	Write(sessionID, ref, input string) error
	Resize(sessionID, ref string, cols, rows uint16) error
	Stop(sessionID, ref string) error
	Subscribe(ctx context.Context, sessionID, ref string, after uint64) (<-chan DataEvent, error)
	CloseSession(sessionID string)
}
