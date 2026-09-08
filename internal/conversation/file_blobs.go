package conversation

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"time"
)

const fileBlobCodecGzip = "gzip"

const (
	fileCheckpointLimit     = 100
	fileCheckpointRetention = 30 * 24 * time.Hour
)

func persistEventFileChangeTx(
	ctx context.Context,
	tx *sql.Tx,
	ev Event,
) (Event, error) {
	if ev.Kind != KindMessageEnd && ev.Kind != KindMessageImported {
		return ev, nil
	}
	item, ok := messageFromEvent(ev)
	if !ok || item.FileChange == nil {
		return ev, nil
	}
	change := *item.FileChange
	change.Path = filepath.Clean(change.Path)
	if !filepath.IsAbs(change.Path) || change.AfterBlob == "" {
		return Event{}, errors.New("file change metadata is invalid")
	}
	if change.BeforeExists && change.BeforeBlob == "" {
		return Event{}, errors.New("file change before blob is missing")
	}
	if !change.BeforeExists {
		change.BeforeBlob = ""
	}
	if change.ContentCaptured {
		if change.BeforeExists {
			if err := putFileBlobTx(ctx, tx, change.BeforeBlob, change.BeforeContent); err != nil {
				return Event{}, err
			}
		}
		if err := putFileBlobTx(ctx, tx, change.AfterBlob, change.AfterContent); err != nil {
			return Event{}, err
		}
	} else {
		available, err := fileChangeBlobsExistTx(ctx, tx, change)
		if err != nil {
			return Event{}, err
		}
		if !available {
			if ev.Kind != KindMessageImported {
				return Event{}, errors.New("file change references missing blobs")
			}
			item.FileChange = nil
			ev.Payload = item
			return ev, nil
		}
	}
	change.BeforeContent = nil
	change.AfterContent = nil
	change.ContentCaptured = false
	item.FileChange = &change
	ev.Payload = item
	return ev, nil
}

func (s *store) FileBlob(ctx context.Context, hash string) ([]byte, error) {
	return loadFileBlob(ctx, s.db, hash)
}

func fileChangeBlobsExistTx(
	ctx context.Context,
	tx *sql.Tx,
	change FileChange,
) (bool, error) {
	hashes := []string{change.AfterBlob}
	if change.BeforeExists {
		hashes = append(hashes, change.BeforeBlob)
	}
	for _, hash := range hashes {
		var exists int
		err := tx.QueryRowContext(ctx, `
			SELECT 1 FROM file_blobs WHERE hash = ?
		`, hash).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("check file blob: %w", err)
		}
	}
	return true, nil
}

func loadRewindFileChanges(
	ctx context.Context,
	queryer eventQueryer,
	session string,
	target Seq,
) ([]RewindFileChange, error) {
	available, err := fileCheckpointAvailable(ctx, queryer, session, target, time.Now())
	if err != nil {
		return nil, err
	}
	if !available {
		return nil, nil
	}
	rows, err := queryer.QueryContext(ctx, `
		SELECT
			f.event_seq,
			f.path,
			f.before_exists,
			f.before_mode,
			f.after_mode,
			COALESCE(f.before_blob_hash, ''),
			f.after_blob_hash
		FROM file_changes AS f
		JOIN message_projection AS p ON p.event_seq = f.event_seq
		WHERE f.session_id = ?
		  AND f.event_seq >= ?
		  AND p.active = 1
		ORDER BY f.event_seq
	`, session, int64(target))
	if err != nil {
		return nil, fmt.Errorf("read rewind file changes: %w", err)
	}
	defer rows.Close()

	changes := make([]RewindFileChange, 0)
	for rows.Next() {
		var change RewindFileChange
		var rawSeq int64
		if err := rows.Scan(
			&rawSeq,
			&change.Change.Path,
			&change.Change.BeforeExists,
			&change.Change.BeforeMode,
			&change.Change.AfterMode,
			&change.Change.BeforeBlob,
			&change.Change.AfterBlob,
		); err != nil {
			return nil, fmt.Errorf("scan rewind file change: %w", err)
		}
		change.EventSeq = Seq(rawSeq)
		changes = append(changes, change)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rewind file changes: %w", err)
	}
	return changes, nil
}

func (s *store) PendingFileReview(
	ctx context.Context,
	session string,
) (FileReview, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			f.event_seq,
			f.path,
			f.before_exists,
			f.before_mode,
			f.after_mode,
			COALESCE(f.before_blob_hash, ''),
			f.after_blob_hash
		FROM file_changes AS f
		JOIN message_projection AS p ON p.event_seq = f.event_seq
		WHERE f.session_id = ?
		  AND f.review_state = 'pending'
		  AND p.active = 1
		ORDER BY f.event_seq
	`, session)
	if err != nil {
		return FileReview{}, fmt.Errorf("read pending file review: %w", err)
	}
	defer rows.Close()

	review := FileReview{Changes: make([]RewindFileChange, 0)}
	for rows.Next() {
		var (
			change RewindFileChange
			rawSeq int64
		)
		if err := rows.Scan(
			&rawSeq,
			&change.Change.Path,
			&change.Change.BeforeExists,
			&change.Change.BeforeMode,
			&change.Change.AfterMode,
			&change.Change.BeforeBlob,
			&change.Change.AfterBlob,
		); err != nil {
			return FileReview{}, fmt.Errorf("scan pending file review: %w", err)
		}
		change.EventSeq = Seq(rawSeq)
		review.Changes = append(review.Changes, change)
		review.ThroughSeq = change.EventSeq
	}
	if err := rows.Err(); err != nil {
		return FileReview{}, fmt.Errorf("iterate pending file review: %w", err)
	}
	return review, nil
}

func pruneFileChangesTx(
	ctx context.Context,
	tx *sql.Tx,
	session string,
	now time.Time,
) error {
	var pinned bool
	err := tx.QueryRowContext(ctx, `
		SELECT pinned FROM sessions WHERE id = ?
	`, session).Scan(&pinned)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read session pin state: %w", err)
	}
	if pinned {
		return nil
	}

	var floor sql.NullInt64
	if err := tx.QueryRowContext(ctx, `
		SELECT MIN(event_seq)
		FROM (
			SELECT event_seq
			FROM message_projection
			WHERE session_id = ? AND active = 1 AND role = ?
			ORDER BY event_seq DESC
			LIMIT ?
		)
	`, session, string(RoleUser), fileCheckpointLimit).Scan(&floor); err != nil {
		return fmt.Errorf("read file checkpoint floor: %w", err)
	}
	cutoff := now.Add(-fileCheckpointRetention).UnixNano()
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM file_changes
		WHERE session_id = ?
		  AND (
			(? IS NOT NULL AND event_seq < ?)
			OR event_seq IN (
				SELECT seq FROM events WHERE occurred_at_ns < ?
			)
		  )
	`, session, floor, floor, cutoff); err != nil {
		return fmt.Errorf("prune file changes: %w", err)
	}
	return gcFileBlobsTx(ctx, tx)
}

func (s *store) PruneFileCheckpoints(ctx context.Context, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin file checkpoint pruning: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `
		SELECT DISTINCT session_id FROM file_changes ORDER BY session_id
	`)
	if err != nil {
		return fmt.Errorf("list file checkpoint sessions: %w", err)
	}
	sessionIDs := make([]string, 0)
	for rows.Next() {
		var sessionID string
		if err := rows.Scan(&sessionID); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan file checkpoint session: %w", err)
		}
		sessionIDs = append(sessionIDs, sessionID)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close file checkpoint sessions: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate file checkpoint sessions: %w", err)
	}
	for _, sessionID := range sessionIDs {
		if err := pruneFileChangesTx(ctx, tx, sessionID, now); err != nil {
			return err
		}
	}
	if err := gcFileBlobsTx(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit file checkpoint pruning: %w", err)
	}
	return nil
}

func fileCheckpointAvailable(
	ctx context.Context,
	queryer eventQueryer,
	session string,
	target Seq,
	now time.Time,
) (bool, error) {
	var pinned bool
	err := queryer.QueryRowContext(ctx, `
		SELECT pinned FROM sessions WHERE id = ?
	`, session).Scan(&pinned)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("read session pin state: %w", err)
	}
	if pinned {
		return true, nil
	}

	var occurred int64
	if err := queryer.QueryRowContext(ctx, `
		SELECT occurred_at_ns FROM events WHERE seq = ? AND session_id = ?
	`, int64(target), session).Scan(&occurred); err != nil {
		return false, fmt.Errorf("read checkpoint time: %w", err)
	}
	if occurred < now.Add(-fileCheckpointRetention).UnixNano() {
		return false, nil
	}

	var floor sql.NullInt64
	if err := queryer.QueryRowContext(ctx, `
		SELECT MIN(event_seq)
		FROM (
			SELECT event_seq
			FROM message_projection
			WHERE session_id = ? AND active = 1 AND role = ?
			ORDER BY event_seq DESC
			LIMIT ?
		)
	`, session, string(RoleUser), fileCheckpointLimit).Scan(&floor); err != nil {
		return false, fmt.Errorf("read file checkpoint floor: %w", err)
	}
	return !floor.Valid || int64(target) >= floor.Int64, nil
}

func putFileBlobTx(
	ctx context.Context,
	tx *sql.Tx,
	expectedHash string,
	content []byte,
) error {
	hash := fileBlobHash(content)
	if expectedHash == "" || expectedHash != hash {
		return fmt.Errorf("file blob hash mismatch: expected %q, got %q", expectedHash, hash)
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(content); err != nil {
		return fmt.Errorf("compress file blob: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("finish file blob compression: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO file_blobs(hash, codec, raw_size, data, created_at_ns)
		VALUES (?, ?, ?, ?, ?)
	`, hash, fileBlobCodecGzip, len(content), compressed.Bytes(), time.Now().UnixNano()); err != nil {
		return fmt.Errorf("store file blob: %w", err)
	}
	return nil
}

func loadFileBlob(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	hash string,
) ([]byte, error) {
	var (
		codec   string
		rawSize int64
		data    []byte
	)
	err := queryer.QueryRowContext(ctx, `
		SELECT codec, raw_size, data
		FROM file_blobs
		WHERE hash = ?
	`, hash).Scan(&codec, &rawSize, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrFileBlobNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read file blob: %w", err)
	}
	if codec != fileBlobCodecGzip {
		return nil, fmt.Errorf("unsupported file blob codec %q", codec)
	}
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("open file blob: %w", err)
	}
	content, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil {
		return nil, fmt.Errorf("decompress file blob: %w", readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close file blob: %w", closeErr)
	}
	if int64(len(content)) != rawSize || fileBlobHash(content) != hash {
		return nil, errors.New("file blob integrity check failed")
	}
	return content, nil
}

func gcFileBlobsTx(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM file_blobs
		WHERE NOT EXISTS (
			SELECT 1
			FROM file_changes
			WHERE before_blob_hash = file_blobs.hash
			   OR after_blob_hash = file_blobs.hash
		)
		  AND NOT EXISTS (
			SELECT 1
			FROM file_rewind_journal_files
			WHERE before_blob_hash = file_blobs.hash
			   OR after_blob_hash = file_blobs.hash
		)
	`); err != nil {
		return fmt.Errorf("collect unreferenced file blobs: %w", err)
	}
	return nil
}

func fileBlobHash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
