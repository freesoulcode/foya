package subagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/freesoulcode/foya/internal/agentdef"
	"github.com/freesoulcode/foya/internal/broker"
	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/provider"
	"github.com/freesoulcode/foya/internal/session"
	"github.com/freesoulcode/foya/internal/state"
)

type recordingRunner struct {
	log  state.Store
	task chan string
}

func (r recordingRunner) RunTurn(ctx context.Context, sessionID, task string) error {
	if r.task != nil {
		r.task <- task
	}
	_, err := r.log.Append(ctx, event.Event{
		Kind: event.KindMessageEnd, Session: sessionID,
		Payload: message.Message{Role: message.RoleAssistant, Content: "done: " + task},
	})
	return err
}

func TestStartReturnsImmediatelyAndWaitsForCompletion(t *testing.T) {
	definitions := agentdef.NewManager("", agentdef.BuiltinDefinitions())
	sessions := newTestSessionManager(t)
	parent, _ := sessions.Create(session.CreateOptions{Model: "model"})
	log := newTestStore(t)
	runner := &blockingRunner{
		started: make(chan struct{}, 1),
		release: make(chan struct{}, 1),
	}
	manager := NewManager(
		definitions, sessions, runner, log, log, broker.New[event.Event](), nil,
		Limits{MaxGlobalConcurrency: 1, MaxPerRoot: 1},
	)
	snapshot, err := manager.Start(context.Background(), SpawnRequest{
		ParentSessionID: parent.ID, Task: "task",
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != StatusQueued {
		t.Fatalf("initial status = %s", snapshot.Status)
	}
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("child did not start")
	}
	running, _ := manager.Read(snapshot.ID)
	if running.Status != StatusRunning || running.ChildSessionID == "" {
		t.Fatalf("running snapshot = %+v", running)
	}
	runner.release <- struct{}{}
	items, err := manager.Wait(context.Background(), []string{snapshot.ID}, true)
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Status != StatusFailed {
		t.Fatalf("status = %s, want failed because runner returned no output", items[0].Status)
	}
}

func TestContextSelectionBuildsExplicitTaskPackage(t *testing.T) {
	definitions := agentdef.NewManager("", agentdef.BuiltinDefinitions())
	sessions := newTestSessionManager(t)
	parent, _ := sessions.Create(session.CreateOptions{Model: "model"})
	log := newTestStore(t)
	_, _ = log.Append(context.Background(), event.Event{
		Kind: event.KindMessageEnd, Session: parent.ID,
		Payload: message.Message{Role: message.RoleUser, Content: "first fact"},
	})
	secondSeq, _ := log.Append(context.Background(), event.Event{
		Kind: event.KindMessageEnd, Session: parent.ID,
		Payload: message.Message{Role: message.RoleAssistant, Content: "selected evidence"},
	})
	tasks := make(chan string, 1)
	manager := NewManager(
		definitions, sessions, recordingRunner{log: log, task: tasks}, log, log,
		broker.New[event.Event](), nil, Limits{},
	)
	_, err := manager.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: parent.ID, Task: "analyze",
		Context: ContextSelection{
			Mode: ContextSelected, MessageSeqs: []uint64{uint64(secondSeq)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	task := <-tasks
	if !strings.Contains(task, "selected evidence") || strings.Contains(task, "first fact") {
		t.Fatalf("task package = %q", task)
	}
}

func TestPersistenceMarksRunningAgentInterrupted(t *testing.T) {
	dataDir := t.TempDir()
	definitions := agentdef.NewManager("", agentdef.BuiltinDefinitions())
	sessions := newTestSessionManager(t)
	parent, _ := sessions.Create(session.CreateOptions{Model: "model"})
	log := newTestStore(t)
	runner := &blockingRunner{
		started: make(chan struct{}, 1),
		release: make(chan struct{}, 1),
	}
	manager := NewManager(
		definitions, sessions, runner, log, log, broker.New[event.Event](), nil, Limits{},
	)
	if err := manager.EnablePersistence(dataDir); err != nil {
		t.Fatal(err)
	}
	snapshot, err := manager.Start(context.Background(), SpawnRequest{
		ParentSessionID: parent.ID, Task: "task",
	})
	if err != nil {
		t.Fatal(err)
	}
	<-runner.started

	restored := NewManager(
		definitions, sessions, runner, log, log, broker.New[event.Event](), nil, Limits{},
	)
	if err := restored.EnablePersistence(dataDir); err != nil {
		t.Fatal(err)
	}
	item, err := restored.Read(snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if item.Status != StatusInterrupted {
		t.Fatalf("restored status = %s", item.Status)
	}
	runner.release <- struct{}{}
}

func TestTreeTokenBudgetCancelsActiveRun(t *testing.T) {
	definitions := agentdef.NewManager("", agentdef.BuiltinDefinitions())
	sessions := newTestSessionManager(t)
	parent, _ := sessions.Create(session.CreateOptions{Model: "model"})
	log := newTestStore(t)
	runner := &blockingRunner{
		started: make(chan struct{}, 1),
		release: make(chan struct{}, 1),
	}
	manager := NewManager(
		definitions, sessions, runner, log, log, broker.New[event.Event](), nil,
		Limits{MaxTreeTokens: 10},
	)
	snapshot, err := manager.Start(context.Background(), SpawnRequest{
		ParentSessionID: parent.ID, RootRunID: "root-run", Task: "task",
	})
	if err != nil {
		t.Fatal(err)
	}
	<-runner.started
	running, _ := manager.Read(snapshot.ID)
	manager.ObserveUsage(running.ChildSessionID, provider.Usage{TotalTokens: 11})
	items, err := manager.Wait(context.Background(), []string{snapshot.ID}, true)
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Status != StatusCancelled {
		t.Fatalf("status = %s, want cancelled", items[0].Status)
	}
	budget := manager.Budget(running.ChildSessionID)
	if !budget.Exceeded || budget.TokensUsed != 11 {
		t.Fatalf("budget = %+v", budget)
	}
}

func TestSpawnCreatesIsolatedChildSession(t *testing.T) {
	home := t.TempDir()
	writeAgent(t, filepath.Join(home, ".agents", "agents", "researcher.md"), `---
name: researcher
description: research specialist
model: specialist-model
tools: [read, agent, write]
max_turns: 7
---
Return evidence-backed findings.
`)
	definitions := agentdef.NewManager(home, agentdef.BuiltinDefinitions())
	sessions := newTestSessionManager(t)
	parent, err := sessions.Create(session.CreateOptions{
		ConnectionID: "connection-1", Model: "parent-model",
		ProjectID: "project-1", ApprovalMode: "manual",
	})
	if err != nil {
		t.Fatal(err)
	}
	log := newTestStore(t)
	bus := broker.New[event.Event]()
	manager := NewManager(
		definitions, sessions, recordingRunner{log: log}, log, log, bus,
		func(string) (string, bool) { return "", false },
		Limits{MaxGlobalConcurrency: 2, MaxPerRoot: 2},
	)

	result, err := manager.Spawn(context.Background(), SpawnRequest{
		ParentSessionID:  parent.ID,
		ParentToolCallID: "call-1",
		Task:             "analyze the market",
		AgentRef:         "researcher",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "completed" || result.Output != "done: analyze the market" {
		t.Fatalf("result = %+v", result)
	}
	child, ok := sessions.Get(result.ChildSessionID)
	if !ok {
		t.Fatal("child session missing")
	}
	if child.ParentID != parent.ID || child.SpawnedBy == nil ||
		child.SpawnedBy.ParentToolCallID != "call-1" {
		t.Fatalf("child lineage = %+v", child)
	}
	if child.AgentRef != "user:researcher" || child.AgentDigest == "" {
		t.Fatalf("agent snapshot = %+v", child)
	}
	if child.Model != "specialist-model" || child.AgentMaxTurns != 7 {
		t.Fatalf("child runtime config = %+v", child)
	}
	if len(child.AllowedTools) != 2 || child.AllowedTools[0] != "read" ||
		child.AllowedTools[1] != "write" {
		t.Fatalf("allowed tools = %#v", child.AllowedTools)
	}
}

func TestLifecycleCallbacksCanRejectSubagentCompletion(t *testing.T) {
	definitions := agentdef.NewManager("", agentdef.BuiltinDefinitions())
	sessions := newTestSessionManager(t)
	parent, _ := sessions.Create(session.CreateOptions{Model: "model"})
	log := newTestStore(t)
	manager := NewManager(
		definitions,
		sessions,
		recordingRunner{log: log},
		log,
		log,
		broker.New[event.Event](),
		nil,
		Limits{},
	)
	var started Lifecycle
	var stopped Lifecycle
	manager.SetLifecycleCallbacks(
		func(_ context.Context, lifecycle Lifecycle) {
			started = lifecycle
		},
		func(_ context.Context, lifecycle Lifecycle) (bool, string) {
			stopped = lifecycle
			return true, "validation failed"
		},
	)

	result, err := manager.Spawn(context.Background(), SpawnRequest{
		ParentSessionID: parent.ID,
		RootRunID:       "root-run",
		Task:            "check",
	})
	if err == nil || result.Status != string(StatusFailed) ||
		result.Error != "validation failed" {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
	if started.ChildSessionID == "" || started.ParentSessionID != parent.ID {
		t.Fatalf("started lifecycle = %+v", started)
	}
	if stopped.Status != "completed" || stopped.Output != "done: check" {
		t.Fatalf("stopped lifecycle = %+v", stopped)
	}
}

type blockingRunner struct {
	active    atomic.Int32
	maxActive atomic.Int32
	started   chan struct{}
	release   chan struct{}
}

func (r *blockingRunner) RunTurn(ctx context.Context, _, _ string) error {
	active := r.active.Add(1)
	defer r.active.Add(-1)
	for {
		max := r.maxActive.Load()
		if active <= max || r.maxActive.CompareAndSwap(max, active) {
			break
		}
	}
	r.started <- struct{}{}
	select {
	case <-r.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestManagerEnforcesConcurrencyLimit(t *testing.T) {
	definitions := agentdef.NewManager("", agentdef.BuiltinDefinitions())
	sessions := newTestSessionManager(t)
	parent, _ := sessions.Create(session.CreateOptions{Model: "model"})
	log := newTestStore(t)
	runner := &blockingRunner{
		started: make(chan struct{}, 3),
		release: make(chan struct{}, 3),
	}
	manager := NewManager(
		definitions, sessions, runner, log, log, broker.New[event.Event](),
		nil, Limits{MaxGlobalConcurrency: 2, MaxPerRoot: 2},
	)
	done := make(chan struct{}, 3)
	for i := 0; i < 3; i++ {
		go func() {
			_, _ = manager.Spawn(context.Background(), SpawnRequest{
				ParentSessionID: parent.ID, ParentToolCallID: "call", Task: "task",
			})
			done <- struct{}{}
		}()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-runner.started:
		case <-time.After(time.Second):
			t.Fatal("two child runs did not start")
		}
	}
	select {
	case <-runner.started:
		t.Fatal("third child exceeded concurrency limit")
	case <-time.After(50 * time.Millisecond):
	}
	runner.release <- struct{}{}
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("queued child did not start after a slot was released")
	}
	runner.release <- struct{}{}
	runner.release <- struct{}{}
	for i := 0; i < 3; i++ {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("child run did not finish")
		}
	}
	if runner.maxActive.Load() != 2 {
		t.Fatalf("max active = %d, want 2", runner.maxActive.Load())
	}
}

func TestManagerAppliesConcurrencyIncreaseAtRuntime(t *testing.T) {
	definitions := agentdef.NewManager("", agentdef.BuiltinDefinitions())
	sessions := newTestSessionManager(t)
	parent, _ := sessions.Create(session.CreateOptions{Model: "model"})
	log := newTestStore(t)
	runner := &blockingRunner{
		started: make(chan struct{}, 2),
		release: make(chan struct{}, 2),
	}
	manager := NewManager(
		definitions, sessions, runner, log, log, broker.New[event.Event](), nil,
		Limits{MaxGlobalConcurrency: 1, MaxPerRoot: 1},
	)
	for i := 0; i < 2; i++ {
		_, err := manager.Start(context.Background(), SpawnRequest{
			ParentSessionID: parent.ID, RootRunID: "root-run", Task: "task",
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("first child did not start")
	}
	select {
	case <-runner.started:
		t.Fatal("second child started before the limit changed")
	case <-time.After(50 * time.Millisecond):
	}

	manager.UpdateLimits(Limits{
		MaxGlobalConcurrency: 2,
		MaxPerRoot:           2,
		MaxChildrenPerRoot:   8,
	})
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("queued child did not start after raising the limit")
	}
	runner.release <- struct{}{}
	runner.release <- struct{}{}
}

func writeAgent(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
