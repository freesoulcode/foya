package state

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/freesoulcode/foya/internal/event"
)

const (
	FileRewindPrepared  = "prepared"
	FileRewindCommitted = "committed"
)

func (s *store) BeginFileRewind(
	ctx context.Context,
	session string,
	targetUserSeq event.Seq,
	expectedHeadSeq event.Seq,
	files []FileRewindBackup,
) (string, error) {
	id, err := newFileRewindID()
	if err != nil {
		return "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin file rewind journal: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UnixNano()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO file_rewind_journals(
			id, session_id, target_user_seq, expected_head_seq,
			state, created_at_ns, updated_at_ns
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`, id, session, int64(targetUserSeq), int64(expectedHeadSeq),
		FileRewindPrepared, now, now); err != nil {
		return "", fmt.Errorf("insert file rewind journal: %w", err)
	}
	for _, file := range files {
		path := file.Path
		var beforeBlob, afterBlob any
		if file.BeforeExists {
			hash := fileBlobHash(file.BeforeContent)
			if file.BeforeBlob != "" && file.BeforeBlob != hash {
				return "", fmt.Errorf("file rewind before hash mismatch for %s", path)
			}
			if err := putFileBlobTx(ctx, tx, hash, file.BeforeContent); err != nil {
				return "", err
			}
			beforeBlob = hash
		}
		if file.AfterExists {
			hash := fileBlobHash(file.AfterContent)
			if file.AfterBlob != "" && file.AfterBlob != hash {
				return "", fmt.Errorf("file rewind after hash mismatch for %s", path)
			}
			if err := putFileBlobTx(ctx, tx, hash, file.AfterContent); err != nil {
				return "", err
			}
			afterBlob = hash
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO file_rewind_journal_files(
				journal_id, path,
				before_exists, before_mode, before_blob_hash,
				after_exists, after_mode, after_blob_hash
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, id, path,
			file.BeforeExists, file.BeforeMode, beforeBlob,
			file.AfterExists, file.AfterMode, afterBlob); err != nil {
			return "", fmt.Errorf("insert file rewind journal file: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit file rewind journal: %w", err)
	}
	return id, nil
}

func (s *store) PendingFileRewinds(ctx context.Context) ([]FileRewindJournal, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, session_id, target_user_seq, expected_head_seq, state
		FROM file_rewind_journals
		ORDER BY created_at_ns, id
	`)
	if err != nil {
		return nil, fmt.Errorf("read file rewind journals: %w", err)
	}
	journals := make([]FileRewindJournal, 0)
	for rows.Next() {
		var (
			journal FileRewindJournal
			target  int64
			head    int64
		)
		if err := rows.Scan(
			&journal.ID,
			&journal.SessionID,
			&target,
			&head,
			&journal.State,
		); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan file rewind journal: %w", err)
		}
		journal.TargetUserSeq = event.Seq(target)
		journal.ExpectedHeadSeq = event.Seq(head)
		journals = append(journals, journal)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close file rewind journals: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate file rewind journals: %w", err)
	}

	for index := range journals {
		files, err := s.fileRewindJournalFiles(ctx, journals[index].ID)
		if err != nil {
			return nil, err
		}
		journals[index].Files = files
	}
	return journals, nil
}

func (s *store) fileRewindJournalFiles(
	ctx context.Context,
	journalID string,
) ([]FileRewindBackup, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			path,
			before_exists,
			before_mode,
			COALESCE(before_blob_hash, ''),
			after_exists,
			after_mode,
			COALESCE(after_blob_hash, '')
		FROM file_rewind_journal_files
		WHERE journal_id = ?
		ORDER BY path
	`, journalID)
	if err != nil {
		return nil, fmt.Errorf("read file rewind journal files: %w", err)
	}
	defer rows.Close()

	files := make([]FileRewindBackup, 0)
	for rows.Next() {
		var file FileRewindBackup
		if err := rows.Scan(
			&file.Path,
			&file.BeforeExists,
			&file.BeforeMode,
			&file.BeforeBlob,
			&file.AfterExists,
			&file.AfterMode,
			&file.AfterBlob,
		); err != nil {
			return nil, fmt.Errorf("scan file rewind journal file: %w", err)
		}
		files = append(files, file)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate file rewind journal files: %w", err)
	}
	return files, nil
}

func (s *store) FinishFileRewind(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin file rewind journal cleanup: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM file_rewind_journals WHERE id = ?
	`, id); err != nil {
		return fmt.Errorf("delete file rewind journal: %w", err)
	}
	if err := gcFileBlobsTx(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit file rewind journal cleanup: %w", err)
	}
	return nil
}

func markFileRewindCommittedTx(
	ctx context.Context,
	tx *sql.Tx,
	id string,
	session string,
	targetUserSeq event.Seq,
	expectedHeadSeq event.Seq,
) error {
	if id == "" {
		return nil
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE file_rewind_journals
		SET state = ?, updated_at_ns = ?
		WHERE id = ?
		  AND session_id = ?
		  AND target_user_seq = ?
		  AND expected_head_seq = ?
		  AND state = ?
	`, FileRewindCommitted, time.Now().UnixNano(), id, session,
		int64(targetUserSeq), int64(expectedHeadSeq), FileRewindPrepared)
	if err != nil {
		return fmt.Errorf("mark file rewind committed: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read file rewind journal update count: %w", err)
	}
	if affected != 1 {
		return errors.New("file rewind journal is missing or already completed")
	}
	return nil
}

func newFileRewindID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate file rewind journal id: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}
