package server

import (
	"net/http"
	"testing"

	"github.com/freesoulcode/foya/internal/approval"
	"github.com/freesoulcode/foya/internal/automation"
)

func TestAutomationRoutes(t *testing.T) {
	manager := &fakeAutomationManager{}
	server := &Server{automations: manager, mux: http.NewServeMux()}
	server.routes()
	input := automation.Input{
		Name:         "Daily report",
		Prompt:       "Summarize progress",
		Cron:         "0 9 * * 1-5",
		Timezone:     "UTC",
		Enabled:      true,
		ApprovalMode: approval.ModeAuto,
	}

	var created automation.Task
	if code := requestJSON(
		t,
		server.Handler(),
		http.MethodPost,
		"/automations",
		input,
		&created,
	); code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d", code, http.StatusCreated)
	}
	if created.ID != "automation-1" {
		t.Fatalf("created = %#v", created)
	}

	var started automation.Task
	if code := requestJSON(
		t,
		server.Handler(),
		http.MethodPost,
		"/automations/automation-1/run",
		nil,
		&started,
	); code != http.StatusAccepted {
		t.Fatalf("run status = %d, want %d", code, http.StatusAccepted)
	}
	if started.LastStatus != automation.RunStatusRunning {
		t.Fatalf("started = %#v", started)
	}
}

type fakeAutomationManager struct {
	tasks []automation.Task
}

func (m *fakeAutomationManager) List() []automation.Task {
	return append([]automation.Task(nil), m.tasks...)
}

func (m *fakeAutomationManager) Get(id string) (automation.Task, bool) {
	for _, task := range m.tasks {
		if task.ID == id {
			return task, true
		}
	}
	return automation.Task{}, false
}

func (m *fakeAutomationManager) Create(input automation.Input) (automation.Task, error) {
	task := automation.Task{
		ID: "automation-1", Name: input.Name, Prompt: input.Prompt,
		Cron: input.Cron, Enabled: input.Enabled, ApprovalMode: input.ApprovalMode,
	}
	m.tasks = append(m.tasks, task)
	return task, nil
}

func (m *fakeAutomationManager) Update(id string, input automation.Input) (automation.Task, error) {
	task, ok := m.Get(id)
	if !ok {
		return automation.Task{}, automation.ErrNotFound
	}
	task.Name = input.Name
	return task, nil
}

func (m *fakeAutomationManager) Delete(id string) error {
	if _, ok := m.Get(id); !ok {
		return automation.ErrNotFound
	}
	return nil
}

func (m *fakeAutomationManager) RunNow(id string) (automation.Task, error) {
	task, ok := m.Get(id)
	if !ok {
		return automation.Task{}, automation.ErrNotFound
	}
	task.LastStatus = automation.RunStatusRunning
	return task, nil
}
