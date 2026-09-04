// Package question coordinates agent requests for structured human input.
//
// A tool call blocks in Ask until one client submits the complete answer set,
// the turn is cancelled, or the session is removed. Events make the pending
// batch visible to every connected client.
package question

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/freesoulcode/foya/internal/broker"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/state"
)

var (
	ErrInvalidBatch = errors.New("invalid question batch")
	ErrNotFound     = errors.New("question batch not found")
	ErrCancelled    = errors.New("question batch cancelled")
)

// Option is one suggested response. Recommended is presentation metadata.
type Option struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Recommended bool   `json:"recommended,omitempty"`
}

// Question is one item in a batch presented to the user.
type Question struct {
	ID          string   `json:"id"`
	Question    string   `json:"question"`
	Description string   `json:"description,omitempty"`
	Options     []Option `json:"options,omitempty"`
	AllowCustom bool     `json:"allow_custom,omitempty"`
}

// Batch groups all questions from one ask_user tool invocation.
type Batch struct {
	ID         string     `json:"id"`
	SessionID  string     `json:"session_id"`
	RunID      string     `json:"run_id,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Questions  []Question `json:"questions"`
	CreatedAt  time.Time  `json:"created_at"`
}

// Answer is the response to one Question. Value may be an option label or
// custom free text when AllowCustom is true.
type Answer struct {
	QuestionID string `json:"question_id"`
	Value      string `json:"value"`
}

type resolution struct {
	answers []Answer
	err     error
}

type pendingBatch struct {
	batch  Batch
	result chan resolution
}

// Gateway exposes the blocking request and client-side resolution operations.
type Gateway interface {
	Ask(context.Context, Batch) ([]Answer, error)
	Answer(sessionID, batchID string, answers []Answer) error
	Cancel(sessionID, batchID string) error
	ClearSession(sessionID string)
}

type gateway struct {
	mu      sync.Mutex
	pending map[string]pendingBatch
	bus     *broker.Broker[event.Event]
	log     state.Log
}

func NewGateway(bus *broker.Broker[event.Event], log state.Log) Gateway {
	return &gateway{
		pending: make(map[string]pendingBatch),
		bus:     bus,
		log:     log,
	}
}

// Ask publishes a batch then waits for its one terminal resolution.
func (g *gateway) Ask(ctx context.Context, batch Batch) ([]Answer, error) {
	if err := validateBatch(&batch); err != nil {
		return nil, err
	}
	ch := make(chan resolution, 1)
	g.mu.Lock()
	g.pending[batch.ID] = pendingBatch{batch: batch, result: ch}
	g.mu.Unlock()

	g.publish(ctx, event.KindQuestionRequested, batch.SessionID, batch)
	select {
	case result := <-ch:
		return result.answers, result.err
	case <-ctx.Done():
		g.take(batch.ID)
		g.publish(context.WithoutCancel(ctx), event.KindQuestionResolved, batch.SessionID, map[string]string{
			"id": batch.ID, "status": "cancelled",
		})
		return nil, ctx.Err()
	}
}

// Answer accepts the first complete valid answer set from any client.
func (g *gateway) Answer(sessionID, batchID string, answers []Answer) error {
	g.mu.Lock()
	pending, ok := g.pending[batchID]
	if !ok || pending.batch.SessionID != sessionID {
		g.mu.Unlock()
		return ErrNotFound
	}
	if err := validateAnswers(pending.batch, answers); err != nil {
		g.mu.Unlock()
		return err
	}
	delete(g.pending, batchID)
	g.mu.Unlock()
	pending.result <- resolution{answers: answers}
	g.publish(context.Background(), event.KindQuestionResolved, sessionID, map[string]string{
		"id": batchID, "status": "answered",
	})
	return nil
}

func (g *gateway) Cancel(sessionID, batchID string) error {
	pending, ok := g.take(batchID)
	if !ok || pending.batch.SessionID != sessionID {
		return ErrNotFound
	}
	pending.result <- resolution{err: ErrCancelled}
	g.publish(context.Background(), event.KindQuestionResolved, sessionID, map[string]string{
		"id": batchID, "status": "cancelled",
	})
	return nil
}

// ClearSession releases all waiting tool calls before their session vanishes.
func (g *gateway) ClearSession(sessionID string) {
	g.mu.Lock()
	var pending []pendingBatch
	for id, item := range g.pending {
		if item.batch.SessionID == sessionID {
			delete(g.pending, id)
			pending = append(pending, item)
		}
	}
	g.mu.Unlock()
	for _, item := range pending {
		item.result <- resolution{err: ErrCancelled}
		g.publish(context.Background(), event.KindQuestionResolved, sessionID, map[string]string{
			"id": item.batch.ID, "status": "cancelled",
		})
	}
}

func (g *gateway) take(id string) (pendingBatch, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	item, ok := g.pending[id]
	if ok {
		delete(g.pending, id)
	}
	return item, ok
}

func (g *gateway) publish(ctx context.Context, kind event.Kind, sessionID string, payload any) {
	ev := event.Event{Kind: kind, Session: sessionID, Time: time.Now(), Payload: payload}
	seq, _ := g.log.Append(ctx, ev)
	ev.Seq = seq
	_ = g.bus.PublishMustDeliver(ctx, "session:"+sessionID, ev)
}

func validateBatch(batch *Batch) error {
	if batch.ID == "" {
		batch.ID = newID()
	}
	if strings.TrimSpace(batch.SessionID) == "" {
		return fmt.Errorf("%w: session_id is required", ErrInvalidBatch)
	}
	if len(batch.Questions) == 0 || len(batch.Questions) > 8 {
		return fmt.Errorf("%w: questions must contain 1 to 8 items", ErrInvalidBatch)
	}
	ids := make(map[string]struct{}, len(batch.Questions))
	for i := range batch.Questions {
		item := &batch.Questions[i]
		if item.ID == "" {
			item.ID = fmt.Sprintf("q%d", i+1)
		}
		if strings.TrimSpace(item.Question) == "" {
			return fmt.Errorf("%w: question %q is empty", ErrInvalidBatch, item.ID)
		}
		if _, exists := ids[item.ID]; exists {
			return fmt.Errorf("%w: duplicate question id %q", ErrInvalidBatch, item.ID)
		}
		ids[item.ID] = struct{}{}
		if len(item.Options) > 8 {
			return fmt.Errorf("%w: question %q has more than 8 options", ErrInvalidBatch, item.ID)
		}
	}
	if batch.CreatedAt.IsZero() {
		batch.CreatedAt = time.Now()
	}
	return nil
}

func validateAnswers(batch Batch, answers []Answer) error {
	if len(answers) != len(batch.Questions) {
		return fmt.Errorf("%w: expected %d answers", ErrInvalidBatch, len(batch.Questions))
	}
	questions := make(map[string]Question, len(batch.Questions))
	for _, item := range batch.Questions {
		questions[item.ID] = item
	}
	answered := make(map[string]struct{}, len(answers))
	for _, answer := range answers {
		item, ok := questions[answer.QuestionID]
		if !ok {
			return fmt.Errorf("%w: unknown question %q", ErrInvalidBatch, answer.QuestionID)
		}
		if _, exists := answered[answer.QuestionID]; exists {
			return fmt.Errorf("%w: duplicate answer for %q", ErrInvalidBatch, answer.QuestionID)
		}
		if strings.TrimSpace(answer.Value) == "" {
			return fmt.Errorf("%w: answer for %q is empty", ErrInvalidBatch, answer.QuestionID)
		}
		if !item.AllowCustom && len(item.Options) > 0 {
			found := false
			for _, option := range item.Options {
				if answer.Value == option.Label {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("%w: answer for %q is not an option", ErrInvalidBatch, answer.QuestionID)
			}
		}
		answered[answer.QuestionID] = struct{}{}
	}
	return nil
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
