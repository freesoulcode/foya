// Package channel defines platform-neutral contracts shared by chat adapters.
package channel

import (
	"context"
	"io"

	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/session"
)

// Runtime is the Foya surface needed by external chat adapters.
type Runtime interface {
	CreateSession(session.CreateOptions) (*session.Session, error)
	ListSessions() []*session.Session
	Subscribe(context.Context, string) <-chan event.Event
	SubmitChatInput(context.Context, string, message.UserInput) error
	PutImage(context.Context, string, string, io.Reader) (message.AttachmentRef, error)
	CancelTurn(string)
	ResolveApproval(string, string) error
	CancelQuestions(string, string) error
}
