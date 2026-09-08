// Package channel defines platform-neutral contracts shared by chat adapters.
package channel

import (
	"context"
	"io"

	conversation "github.com/freesoulcode/foya/internal/conversation"
)

// Runtime is the Foya surface needed by external chat adapters.
type Runtime interface {
	CreateSession(conversation.CreateOptions) (*conversation.Session, error)
	ListSessions() []*conversation.Session
	Subscribe(context.Context, string) <-chan conversation.Event
	SubmitChatInput(context.Context, string, conversation.UserInput) error
	PutImage(context.Context, string, string, io.Reader) (conversation.AttachmentRef, error)
	CancelTurn(string)
	ResolveApproval(string, string) error
	CancelQuestions(string, string) error
}
