package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/freesoulcode/foya/internal/approval"
	"github.com/freesoulcode/foya/internal/storage"
)

// sqliteManager persists session metadata in the shared Foya database.
type sqliteManager struct {
	db *storage.Database
}

// NewSQLiteManager creates a session manager backed by SQLite. Runtime phases
// are reset because in-flight turns are recovered separately.
func NewSQLiteManager(db *storage.Database) (Manager, error) {
	if _, err := db.Exec(`
		UPDATE sessions
		SET phase = ?,
		    agent_mode = CASE WHEN agent_mode = '' THEN ? ELSE agent_mode END
	`, string(PhaseIdle), string(AgentModeExecute)); err != nil {
		return nil, fmt.Errorf("reset session runtime state: %w", err)
	}
	return &sqliteManager{db: db}, nil
}

func (m *sqliteManager) Create(opts CreateOptions) (*Session, error) {
	if err := validateCreateOptions(opts); err != nil {
		return nil, err
	}
	now := time.Now()
	item := &Session{
		ID:                newID(),
		ParentID:          opts.ParentID,
		SpawnedBy:         cloneSpawnedBy(opts.SpawnedBy),
		AgentRef:          opts.AgentRef,
		AgentName:         opts.AgentName,
		AgentDigest:       opts.AgentDigest,
		Phase:             PhaseIdle,
		AgentMode:         AgentModeExecute,
		ConnectionID:      opts.ConnectionID,
		Model:             opts.Model,
		ReasoningEffort:   opts.ReasoningEffort,
		ProjectID:         opts.ProjectID,
		ApprovalMode:      opts.ApprovalMode,
		CreatedAt:         now,
		UpdatedAt:         now,
		AgentInstructions: opts.AgentInstructions,
		AllowedTools:      append([]string(nil), opts.AllowedTools...),
		AgentMaxTurns:     opts.AgentMaxTurns,
	}
	if err := insertSession(context.Background(), m.db, item); err != nil {
		return nil, err
	}
	return cloneSession(item), nil
}

func (m *sqliteManager) Get(id string) (*Session, bool) {
	item, err := readSession(context.Background(), m.db, id)
	if err != nil {
		return nil, false
	}
	return item, true
}

func (m *sqliteManager) List() []*Session {
	rows, err := m.db.QueryContext(context.Background(), `
		SELECT `+sessionColumns+`
		FROM sessions
		ORDER BY updated_at_ns DESC, id
	`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	items := make([]*Session, 0)
	for rows.Next() {
		item, scanErr := scanSession(rows)
		if scanErr != nil {
			return nil
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return nil
	}
	return items
}

func (m *sqliteManager) Update(
	id string,
	connectionID, model, reasoningEffort, projectID, approvalMode *string,
) (*Session, error) {
	return m.mutate(id, func(item *Session) error {
		if reasoningEffort != nil && !ValidReasoningEffort(*reasoningEffort) {
			return ErrInvalidReasoningEffort
		}
		if approvalMode != nil &&
			!approval.ValidMode(approval.Mode(*approvalMode)) {
			return fmt.Errorf("%w: %q", ErrInvalidApprovalMode, *approvalMode)
		}
		if projectID != nil && item.ProjectID != "" &&
			*projectID != item.ProjectID {
			return ErrProjectLocked
		}
		if model != nil {
			item.Model = *model
		}
		if connectionID != nil {
			item.ConnectionID = *connectionID
		}
		if reasoningEffort != nil {
			item.ReasoningEffort = ReasoningEffort(*reasoningEffort)
		}
		if projectID != nil {
			item.ProjectID = *projectID
		}
		if approvalMode != nil {
			item.ApprovalMode = *approvalMode
		}
		return nil
	})
}

func (m *sqliteManager) SetPhase(id string, phase Phase) (*Session, error) {
	return m.mutate(id, func(item *Session) error {
		item.Phase = phase
		return nil
	})
}

func (m *sqliteManager) SetAgentMode(
	id string,
	mode, prePlanMode AgentMode,
) (*Session, error) {
	switch mode {
	case AgentModeExecute, AgentModePlan, AgentModePlanReady:
	default:
		return nil, fmt.Errorf("invalid agent mode %q", mode)
	}
	return m.mutate(id, func(item *Session) error {
		item.AgentMode = mode
		item.PrePlanMode = prePlanMode
		return nil
	})
}

func (m *sqliteManager) SetTasks(id string, tasks []Task) (*Session, error) {
	return m.mutate(id, func(item *Session) error {
		item.Tasks = append([]Task(nil), tasks...)
		return nil
	})
}

func (m *sqliteManager) SetGeneratedTitle(id, title string) (bool, error) {
	changed := false
	_, err := m.mutate(id, func(item *Session) error {
		if item.Title != "" || item.TitleIsManual {
			return errSessionUnchanged
		}
		item.Title = title
		changed = true
		return nil
	})
	if errors.Is(err, errSessionUnchanged) {
		return false, nil
	}
	return changed, err
}

func (m *sqliteManager) ResetGeneratedTitle(
	id string,
) (*Session, bool, error) {
	changed := false
	item, err := m.mutate(id, func(item *Session) error {
		if item.TitleIsManual || item.Title == "" {
			return errSessionUnchanged
		}
		item.Title = ""
		changed = true
		return nil
	})
	if errors.Is(err, errSessionUnchanged) {
		current, ok := m.Get(id)
		if !ok {
			return nil, false, ErrNotFound
		}
		return current, false, nil
	}
	return item, changed, err
}

func (m *sqliteManager) Rename(id, title string) error {
	_, err := m.mutate(id, func(item *Session) error {
		item.Title = title
		item.TitleIsManual = true
		return nil
	})
	return err
}

func (m *sqliteManager) SetPinned(id string, pinned bool) (*Session, error) {
	return m.mutate(id, func(item *Session) error {
		item.Pinned = pinned
		if pinned {
			now := time.Now()
			item.PinnedAt = &now
		} else {
			item.PinnedAt = nil
		}
		return nil
	})
}

func (m *sqliteManager) Delete(id string) error {
	result, err := m.db.ExecContext(
		context.Background(),
		`DELETE FROM sessions WHERE id = ?`,
		id,
	)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read deleted session count: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (m *sqliteManager) Close(id string) error {
	return m.Delete(id)
}

func (m *sqliteManager) mutate(
	id string,
	change func(*Session) error,
) (*Session, error) {
	ctx := context.Background()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin session update: %w", err)
	}
	defer tx.Rollback()

	item, err := readSession(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if err := change(item); err != nil {
		return nil, err
	}
	item.UpdatedAt = time.Now()
	if err := updateSession(ctx, tx, item); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit session update: %w", err)
	}
	return cloneSession(item), nil
}

type sessionScanner interface {
	Scan(...any) error
}

type sessionQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type sessionExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

const sessionColumns = `
	id, parent_id, spawned_by_json, agent_ref, agent_name, agent_digest,
	phase, agent_mode, pre_plan_mode, connection_id, model,
	reasoning_effort, project_id, approval_mode, title, title_is_manual,
	pinned, pinned_at_ns, tasks_json, created_at_ns, updated_at_ns,
	agent_instructions, allowed_tools_json, agent_max_turns
`

func readSession(
	ctx context.Context,
	queryer sessionQueryer,
	id string,
) (*Session, error) {
	row := queryer.QueryRowContext(ctx, `
		SELECT `+sessionColumns+`
		FROM sessions
		WHERE id = ?
	`, id)
	item, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return item, err
}

func scanSession(scanner sessionScanner) (*Session, error) {
	var (
		item             Session
		spawnedByJSON    []byte
		phase            string
		agentMode        string
		prePlanMode      string
		reasoningEffort  string
		titleIsManual    int
		pinned           int
		pinnedAt         sql.NullInt64
		tasksJSON        []byte
		createdAt        int64
		updatedAt        int64
		allowedToolsJSON []byte
	)
	err := scanner.Scan(
		&item.ID, &item.ParentID, &spawnedByJSON, &item.AgentRef,
		&item.AgentName, &item.AgentDigest, &phase, &agentMode,
		&prePlanMode, &item.ConnectionID, &item.Model, &reasoningEffort,
		&item.ProjectID, &item.ApprovalMode, &item.Title, &titleIsManual,
		&pinned, &pinnedAt, &tasksJSON, &createdAt, &updatedAt,
		&item.AgentInstructions, &allowedToolsJSON, &item.AgentMaxTurns,
	)
	if err != nil {
		return nil, err
	}
	if string(spawnedByJSON) != "null" {
		if err := json.Unmarshal(spawnedByJSON, &item.SpawnedBy); err != nil {
			return nil, fmt.Errorf("decode session provenance: %w", err)
		}
	}
	if err := json.Unmarshal(tasksJSON, &item.Tasks); err != nil {
		return nil, fmt.Errorf("decode session tasks: %w", err)
	}
	if err := json.Unmarshal(allowedToolsJSON, &item.AllowedTools); err != nil {
		return nil, fmt.Errorf("decode session tools: %w", err)
	}
	item.Phase = Phase(phase)
	item.AgentMode = AgentMode(agentMode)
	item.PrePlanMode = AgentMode(prePlanMode)
	item.ReasoningEffort = ReasoningEffort(reasoningEffort)
	item.TitleIsManual = titleIsManual != 0
	item.Pinned = pinned != 0
	if pinnedAt.Valid {
		value := time.Unix(0, pinnedAt.Int64)
		item.PinnedAt = &value
	}
	item.CreatedAt = time.Unix(0, createdAt)
	item.UpdatedAt = time.Unix(0, updatedAt)
	return &item, nil
}

func insertSession(
	ctx context.Context,
	execer sessionExecer,
	item *Session,
) error {
	values, err := encodeSession(item)
	if err != nil {
		return err
	}
	_, err = execer.ExecContext(ctx, `
		INSERT INTO sessions(
			id, parent_id, spawned_by_json, agent_ref, agent_name, agent_digest,
			phase, agent_mode, pre_plan_mode, connection_id, model,
			reasoning_effort, project_id, approval_mode, title, title_is_manual,
			pinned, pinned_at_ns, tasks_json, created_at_ns, updated_at_ns,
			agent_instructions, allowed_tools_json, agent_max_turns
		) VALUES (
			?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
		)
	`, values...)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

func updateSession(
	ctx context.Context,
	execer sessionExecer,
	item *Session,
) error {
	values, err := encodeSession(item)
	if err != nil {
		return err
	}
	values = append(values[1:], values[0])
	result, err := execer.ExecContext(ctx, `
		UPDATE sessions SET
			parent_id = ?, spawned_by_json = ?, agent_ref = ?,
			agent_name = ?, agent_digest = ?, phase = ?, agent_mode = ?,
			pre_plan_mode = ?, connection_id = ?, model = ?,
			reasoning_effort = ?, project_id = ?, approval_mode = ?,
			title = ?, title_is_manual = ?, pinned = ?, pinned_at_ns = ?,
			tasks_json = ?, created_at_ns = ?, updated_at_ns = ?,
			agent_instructions = ?, allowed_tools_json = ?, agent_max_turns = ?
		WHERE id = ?
	`, values...)
	if err != nil {
		return fmt.Errorf("update session: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read updated session count: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func encodeSession(item *Session) ([]any, error) {
	spawnedBy, err := json.Marshal(item.SpawnedBy)
	if err != nil {
		return nil, fmt.Errorf("encode session provenance: %w", err)
	}
	tasks, err := json.Marshal(item.Tasks)
	if err != nil {
		return nil, fmt.Errorf("encode session tasks: %w", err)
	}
	allowedTools, err := json.Marshal(item.AllowedTools)
	if err != nil {
		return nil, fmt.Errorf("encode session tools: %w", err)
	}
	var pinnedAt any
	if item.PinnedAt != nil {
		pinnedAt = item.PinnedAt.UnixNano()
	}
	return []any{
		item.ID, item.ParentID, spawnedBy, item.AgentRef, item.AgentName,
		item.AgentDigest, string(item.Phase), string(item.AgentMode),
		string(item.PrePlanMode), item.ConnectionID, item.Model,
		string(item.ReasoningEffort), item.ProjectID, item.ApprovalMode,
		item.Title, boolInt(item.TitleIsManual), boolInt(item.Pinned), pinnedAt,
		tasks, item.CreatedAt.UnixNano(), item.UpdatedAt.UnixNano(),
		item.AgentInstructions, allowedTools, item.AgentMaxTurns,
	}, nil
}

func cloneSession(item *Session) *Session {
	if item == nil {
		return nil
	}
	cloned := *item
	cloned.SpawnedBy = cloneSpawnedBy(item.SpawnedBy)
	cloned.Tasks = append([]Task(nil), item.Tasks...)
	cloned.AllowedTools = append([]string(nil), item.AllowedTools...)
	if item.PinnedAt != nil {
		value := *item.PinnedAt
		cloned.PinnedAt = &value
	}
	return &cloned
}

func cloneSpawnedBy(value *SpawnedBy) *SpawnedBy {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

var errSessionUnchanged = errors.New("session unchanged")

var _ Manager = (*sqliteManager)(nil)
