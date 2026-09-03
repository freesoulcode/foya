package automation

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/freesoulcode/foya/internal/approval"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/session"
)

func TestManagerCreateUpdateAndPersist(t *testing.T) {
	manager, err := NewManager(context.Background(), t.TempDir(), newFakeRuntime(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	task, err := manager.Create(Input{
		Name:         "Daily report",
		Prompt:       "Summarize progress",
		Cron:         "0 9 * * 1-5",
		Timezone:     "UTC",
		Enabled:      true,
		ApprovalMode: approval.ModeAuto,
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.ID == "" || task.NextRunAt == nil {
		t.Fatalf("created task = %#v", task)
	}

	task, err = manager.Update(task.ID, Input{
		Name:         task.Name,
		Prompt:       task.Prompt,
		Cron:         task.Cron,
		Timezone:     task.Timezone,
		Enabled:      false,
		ApprovalMode: approval.ModeAuto,
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.Enabled || task.NextRunAt != nil {
		t.Fatalf("paused task = %#v", task)
	}
}

func TestManagerRunNowCreatesSessionAndCompletes(t *testing.T) {
	runtime := newFakeRuntime()
	manager, err := NewManager(context.Background(), t.TempDir(), runtime, false)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	task, err := manager.Create(Input{
		Name:         "Run now",
		Prompt:       "Do the thing",
		Cron:         "0 9 * * *",
		Timezone:     "UTC",
		ApprovalMode: approval.ModeAuto,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RunNow(task.ID); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		current, ok := manager.Get(task.ID)
		if ok && current.LastStatus == RunStatusCompleted {
			if current.LastSessionID == "" {
				t.Fatal("completed run has no session")
			}
			if runtime.lastInput.Text != "Do the thing" {
				t.Fatalf("submitted prompt = %q", runtime.lastInput.Text)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("automation did not complete")
}

func TestManagerRejectsInvalidCron(t *testing.T) {
	manager, err := NewManager(context.Background(), t.TempDir(), newFakeRuntime(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if _, err := manager.Create(Input{
		Name:         "Invalid",
		Prompt:       "Run",
		Cron:         "not a cron",
		Timezone:     "UTC",
		ApprovalMode: approval.ModeAuto,
	}); err == nil {
		t.Fatal("invalid cron was accepted")
	}
}

type fakeRuntime struct {
	mu          sync.Mutex
	subscribers map[string]chan event.Event
	lastInput   message.UserInput
}

func newFakeRuntime() *fakeRuntime {
	return &fakeRuntime{subscribers: make(map[string]chan event.Event)}
}

func (r *fakeRuntime) CreateSession(session.CreateOptions) (*session.Session, error) {
	return &session.Session{ID: "session-1"}, nil
}

func (r *fakeRuntime) RenameSession(
	context.Context,
	string,
	string,
) (*session.Session, error) {
	return &session.Session{ID: "session-1"}, nil
}

func (r *fakeRuntime) Subscribe(_ context.Context, sessionID string) <-chan event.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	events := make(chan event.Event, 2)
	r.subscribers[sessionID] = events
	return events
}

func (r *fakeRuntime) SubmitChatInput(
	_ context.Context,
	sessionID string,
	input message.UserInput,
) error {
	r.mu.Lock()
	r.lastInput = input
	events := r.subscribers[sessionID]
	r.mu.Unlock()
	events <- event.Event{Kind: event.KindTurnComplete}
	return nil
}

func (r *fakeRuntime) CancelQuestions(string, string) error {
	return nil
}

var _ Runtime = (*fakeRuntime)(nil)
