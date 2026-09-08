package subagent

import (
	"context"

	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"time"

	conversation "github.com/freesoulcode/foya/internal/conversation"
)

// EnablePersistence restores run snapshots and marks unfinished work as
// interrupted. It never replays an agent or tool call.
func (m *Manager) EnablePersistence(dataDir string) error {
	if dataDir == "" {
		return nil
	}
	path := filepath.Join(dataDir, "agent-runs.json")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	var snapshots []Snapshot
	if len(data) > 0 {
		if err := json.Unmarshal(data, &snapshots); err != nil {
			return err
		}
	}
	now := time.Now()
	var interrupted []Snapshot
	m.mu.Lock()
	m.persistPath = path
	for _, snapshot := range snapshots {
		if snapshot.Status == StatusQueued || snapshot.Status == StatusRunning {
			snapshot.Status = StatusInterrupted
			snapshot.Error = "kernel restarted before the agent run completed"
			snapshot.CompletedAt = &now
			interrupted = append(interrupted, snapshot)
		}
		done := make(chan struct{})
		close(done)
		m.runs[snapshot.ID] = &runState{
			snapshot: snapshot, done: done, cancel: func() {},
		}
		if snapshot.TokensUsed > m.treeTokens[snapshot.RootRunID] {
			m.treeTokens[snapshot.RootRunID] = snapshot.TokensUsed
		}
	}
	m.mu.Unlock()
	m.persist()
	for _, snapshot := range interrupted {
		m.emit(context.Background(), snapshot.ParentSessionID, conversation.KindSubAgentInterrupted, snapshot)
	}
	return nil
}

func (m *Manager) persist() {
	m.persistMu.Lock()
	defer m.persistMu.Unlock()
	m.mu.RLock()
	path := m.persistPath
	states := make([]*runState, 0, len(m.runs))
	for _, state := range m.runs {
		states = append(states, state)
	}
	m.mu.RUnlock()
	if path == "" {
		return
	}
	snapshots := make([]Snapshot, 0, len(states))
	for _, state := range states {
		snapshots = append(snapshots, state.snapshotCopy())
	}
	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].CreatedAt.Before(snapshots[j].CreatedAt)
	})
	data, err := json.MarshalIndent(snapshots, "", "  ")
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, data, 0o600) == nil {
		_ = os.Rename(tmp, path)
	}
}

func (m *Manager) resolveDefinition(
	ctx context.Context,
	parent *conversation.Session,
	ref string,
) (Definition, error) {
	if strings.TrimSpace(ref) == "" {
		return WorkerDefinition(), nil
	}
	projectPath := ""
	if parent.ProjectID != "" && m.resolveProject != nil {
		projectPath, _ = m.resolveProject(parent.ProjectID)
	}
	definition, err := m.definitions.Get(ctx, parent.ProjectID, projectPath, ref)
	if errors.Is(err, fs.ErrNotExist) {
		return Definition{}, fmt.Errorf("unknown agent %q", ref)
	}
	return definition, err
}

func (m *Manager) finalOutput(ctx context.Context, sessionID string) string {
	history, err := m.history.History(ctx, sessionID)
	if err != nil {
		return ""
	}
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == conversation.RoleAssistant && strings.TrimSpace(history[i].Content) != "" {
			return history[i].Content
		}
	}
	return ""
}

func (m *Manager) emit(ctx context.Context, sessionID string, kind conversation.Kind, payload any) {
	ev := conversation.Event{Kind: kind, Session: sessionID, Time: time.Now(), Payload: payload}
	seq, _ := m.log.Append(ctx, ev)
	ev.Seq = seq
	_ = m.bus.PublishMustDeliver(ctx, "session:"+sessionID, ev)
}
