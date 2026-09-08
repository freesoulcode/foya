//go:build !windows

package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	interaction "github.com/freesoulcode/foya/internal/interaction"
	"github.com/freesoulcode/foya/internal/sandbox"
)

func TestBashBackgroundCommandCanBeReadAndStopped(t *testing.T) {
	runner := sandbox.NewRunner()
	manager := NewBackgroundCommandManager(runner)
	bash := NewBashToolWithManager(allowGateway{}, runner, manager)
	ctx := WithSessionID(
		interaction.WithMode(context.Background(), interaction.ModeFullAccess),
		"session-1",
	)
	result, err := bash.Run(ctx, Call{Input: []byte(
		`{"command":"printf ready; sleep 30","background":true}`,
	)})
	if err != nil || result.IsError {
		t.Fatalf("start result = %#v, err = %v", result, err)
	}
	var response struct {
		Status      string                    `json:"status"`
		InitiatedBy string                    `json:"initiated_by"`
		Command     BackgroundCommandSnapshot `json:"command"`
	}
	if err := json.Unmarshal([]byte(result.Content[0].Text), &response); err != nil {
		t.Fatal(err)
	}
	started := response.Command
	if started.ID == "" || !started.Running {
		t.Fatalf("started command = %#v", started)
	}
	if response.InitiatedBy != "agent" || started.BackgroundedBy != "agent" {
		t.Fatalf("background origin = response %q, command %q", response.InitiatedBy, started.BackgroundedBy)
	}

	statusTool := NewBashStatusTool(manager)
	status, err := statusTool.Run(ctx, Call{Input: []byte(
		`{"command_id":"` + started.ID + `"}`,
	)})
	if err != nil || status.IsError {
		t.Fatalf("status result = %#v, err = %v", status, err)
	}

	cancelTool := NewBashCancelTool(manager)
	cancelled, err := cancelTool.Run(ctx, Call{Input: []byte(
		`{"command_id":"` + started.ID + `"}`,
	)})
	if err != nil || cancelled.IsError {
		t.Fatalf("cancel result = %#v, err = %v", cancelled, err)
	}
	if !strings.Contains(cancelled.Content[0].Text, `"running":false`) {
		t.Fatalf("cancelled command = %s", cancelled.Content[0].Text)
	}
	if !strings.Contains(cancelled.Content[0].Text, `"stopped_by":"agent"`) {
		t.Fatalf("cancel origin missing: %s", cancelled.Content[0].Text)
	}
}

func TestForegroundCommandCanBePromotedWithoutRestart(t *testing.T) {
	runner := sandbox.NewRunner()
	manager := NewBackgroundCommandManager(runner)
	bash := NewBashToolWithManager(allowGateway{}, runner, manager)
	ctx := WithSessionID(
		interaction.WithMode(context.Background(), interaction.ModeFullAccess),
		"session-1",
	)
	done := make(chan Result, 1)
	go func() {
		result, _ := bash.Run(ctx, Call{
			ID: "tool-1", Input: []byte(`{"command":"sleep 30"}`),
		})
		done <- result
	}()

	concrete := manager
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		concrete.mu.RLock()
		active := len(concrete.active)
		concrete.mu.RUnlock()
		if active == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if listed := manager.List("session-1"); len(listed) != 0 {
		t.Fatalf("foreground commands must not appear in background list: %#v", listed)
	}

	var promoted BackgroundCommandSnapshot
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		var err error
		promoted, err = manager.Promote("session-1", "tool-1")
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if promoted.ID == "" {
		t.Fatal("foreground command was not promotable")
	}
	select {
	case result := <-done:
		if result.IsError {
			t.Fatalf("foreground result = %#v", result)
		}
		var response struct {
			Status      string                    `json:"status"`
			InitiatedBy string                    `json:"initiated_by"`
			Message     string                    `json:"message"`
			Command     BackgroundCommandSnapshot `json:"command"`
		}
		if err := json.Unmarshal([]byte(result.Content[0].Text), &response); err != nil {
			t.Fatal(err)
		}
		if response.Status != "running_in_background" ||
			response.InitiatedBy != "user" ||
			response.Command.ID != promoted.ID ||
			!strings.Contains(response.Message, "user moved") {
			t.Fatalf("foreground result = %#v", response)
		}
	case <-time.After(time.Second):
		t.Fatal("foreground tool did not return after promotion")
	}
	listed := manager.List("session-1")
	if len(listed) != 1 || listed[0].BackgroundedBy != "user" {
		t.Fatalf("listed commands = %#v", listed)
	}
	stopped, err := manager.Stop("session-1", promoted.ID, "user")
	if err != nil {
		t.Fatal(err)
	}
	if stopped.StoppedBy != "user" {
		t.Fatalf("stop origin = %q", stopped.StoppedBy)
	}
	if listed := manager.List("session-1"); len(listed) != 0 {
		t.Fatalf("finished commands should not remain visible: %#v", listed)
	}
	stored, err := manager.Get("session-1", promoted.ID)
	if err != nil || stored.StoppedBy != "user" {
		t.Fatalf("stored command = %#v, err = %v", stored, err)
	}
}

func TestForegroundCommandCanBeRevealedWithoutPromotion(t *testing.T) {
	runner := sandbox.NewRunner()
	manager := NewBackgroundCommandManager(runner)
	bash := NewBashToolWithManager(allowGateway{}, runner, manager)
	ctx := WithSessionID(
		interaction.WithMode(context.Background(), interaction.ModeFullAccess),
		"session-1",
	)
	done := make(chan Result, 1)
	go func() {
		result, _ := bash.Run(ctx, Call{
			ID: "tool-1", Input: []byte(`{"command":"printf ready; sleep 0.3"}`),
		})
		done <- result
	}()

	var revealed BackgroundCommandSnapshot
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		var err error
		revealed, err = manager.Reveal("session-1", "tool-1")
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if revealed.ID == "" || revealed.BackgroundedBy != "" {
		t.Fatalf("revealed command = %#v", revealed)
	}
	if listed := manager.List("session-1"); len(listed) != 0 {
		t.Fatalf("revealing must not background the command: %#v", listed)
	}
	select {
	case result := <-done:
		t.Fatalf("revealing unexpectedly completed the tool: %#v", result)
	case <-time.After(50 * time.Millisecond):
	}

	select {
	case result := <-done:
		if result.IsError || !strings.Contains(result.Content[0].Text, "ready") {
			t.Fatalf("foreground result = %#v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("foreground command did not complete")
	}
	stored, err := manager.Get("session-1", revealed.ID)
	if err != nil || stored.Running || !strings.Contains(stored.Stdout, "ready") {
		t.Fatalf("stored command = %#v, err = %v", stored, err)
	}
}
