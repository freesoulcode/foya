package backend

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/freesoulcode/foya/internal/event"
	"github.com/freesoulcode/foya/internal/message"
	"github.com/freesoulcode/foya/internal/state"
	"github.com/freesoulcode/foya/internal/tool"
)

const maxThreeWayMergeCells = 4_000_000

type fileSnapshot struct {
	exists  bool
	content []byte
	mode    fs.FileMode
}

type fileRewindPlan struct {
	path   string
	before fileSnapshot
	after  fileSnapshot
}

type fileRewindCandidate struct {
	key       string
	path      string
	status    string
	additions int
	deletions int
	diff      string
	current   fileSnapshot
	safePlan  fileRewindPlan
	forcePlan fileRewindPlan
}

type lineEdit struct {
	start       int
	end         int
	replacement []string
}

func (b *Backend) displayRewindFiles(
	sessionID string,
	candidates []fileRewindCandidate,
) []RewindFilePreview {
	root := ""
	if current, ok := b.sessions.Get(sessionID); ok && current.ProjectID != "" {
		if project, err := b.Project(current.ProjectID); err == nil {
			root = filepath.Clean(project.Path)
		}
	}
	display := make([]RewindFilePreview, 0, len(candidates))
	for _, candidate := range candidates {
		path := candidate.path
		if root != "" {
			if relative, err := filepath.Rel(root, path); err == nil &&
				relative != ".." &&
				!strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				path = relative
			}
		}
		display = append(display, RewindFilePreview{
			Key:       candidate.key,
			Path:      filepath.ToSlash(path),
			Status:    candidate.status,
			Additions: candidate.additions,
			Deletions: candidate.deletions,
			Diff:      candidate.diff,
		})
	}
	return display
}

func inspectFileRewind(
	ctx context.Context,
	blobs state.Store,
	changes []state.RewindFileChange,
) ([]fileRewindCandidate, string, error) {
	grouped := make(map[string][]state.RewindFileChange)
	order := make([]string, 0)
	for _, item := range changes {
		path := filepath.Clean(item.Change.Path)
		if !filepath.IsAbs(path) || item.Change.AfterBlob == "" {
			return nil, "", fmt.Errorf("%w: invalid file change", ErrFileRewindConflict)
		}
		if _, exists := grouped[path]; !exists {
			order = append(order, path)
		}
		grouped[path] = append(grouped[path], item)
	}

	candidates := make([]fileRewindCandidate, 0, len(order))
	var token strings.Builder
	for _, path := range order {
		items := grouped[path]
		current, err := readFileSnapshot(path)
		if err != nil {
			return nil, "", err
		}
		forceTarget, err := beforeFileSnapshot(ctx, blobs, items[0].Change)
		if err != nil {
			return nil, "", err
		}
		recordedAfter, err := afterFileSnapshot(ctx, blobs, items[len(items)-1].Change)
		if err != nil {
			return nil, "", err
		}

		candidate := fileRewindCandidate{
			key:     contentHash([]byte(path)),
			path:    path,
			status:  RewindFileModified,
			current: current,
			forcePlan: fileRewindPlan{
				path:   path,
				before: current,
				after:  forceTarget,
			},
		}
		candidate.diff = tool.UnifiedDiff(
			path,
			string(forceTarget.content),
			string(recordedAfter.content),
		)
		candidate.additions, candidate.deletions = unifiedDiffStats(candidate.diff)

		target := current
		mergedAny := false
		canRestore := true
		for index := len(items) - 1; index >= 0; index-- {
			before, err := beforeFileSnapshot(ctx, blobs, items[index].Change)
			if err != nil {
				return nil, "", err
			}
			after, err := afterFileSnapshot(ctx, blobs, items[index].Change)
			if err != nil {
				return nil, "", err
			}
			if snapshotsEqual(target, after) {
				target = before
				continue
			}
			merged, ok := threeWayRestore(after, target, before)
			if !ok {
				canRestore = false
				break
			}
			target = merged
			mergedAny = true
		}
		if canRestore {
			candidate.status = RewindFileReady
			if mergedAny {
				candidate.status = RewindFileMergeable
			}
			candidate.safePlan = fileRewindPlan{
				path:   path,
				before: current,
				after:  target,
			}
		}
		candidates = append(candidates, candidate)
		fmt.Fprintf(
			&token,
			"%s\x00%s\x00%t\x00%o\x00%s\n",
			candidate.key,
			candidate.status,
			current.exists,
			current.mode.Perm(),
			contentHash(current.content),
		)
	}
	return candidates, contentHash([]byte(token.String())), nil
}

func unifiedDiffStats(diff string) (additions int, deletions int) {
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++ "), strings.HasPrefix(line, "--- "):
		case strings.HasPrefix(line, "+"):
			additions++
		case strings.HasPrefix(line, "-"):
			deletions++
		}
	}
	return additions, deletions
}

func beforeFileSnapshot(
	ctx context.Context,
	blobs state.Store,
	change message.FileChange,
) (fileSnapshot, error) {
	if !change.BeforeExists {
		return fileSnapshot{}, nil
	}
	content, err := blobs.FileBlob(ctx, change.BeforeBlob)
	if err != nil {
		return fileSnapshot{}, fmt.Errorf("load before snapshot for %s: %w", change.Path, err)
	}
	return fileSnapshot{
		exists:  true,
		content: content,
		mode:    fs.FileMode(change.BeforeMode).Perm(),
	}, nil
}

func afterFileSnapshot(
	ctx context.Context,
	blobs state.Store,
	change message.FileChange,
) (fileSnapshot, error) {
	content, err := blobs.FileBlob(ctx, change.AfterBlob)
	if err != nil {
		return fileSnapshot{}, fmt.Errorf("load after snapshot for %s: %w", change.Path, err)
	}
	return fileSnapshot{
		exists:  true,
		content: content,
		mode:    fs.FileMode(change.AfterMode).Perm(),
	}, nil
}

func applyFileRewind(
	candidates []fileRewindCandidate,
	forceKeys []string,
) ([]event.RewindFileResult, func() error, error) {
	results, plans, err := selectFileRewindPlans(candidates, forceKeys)
	if err != nil {
		return nil, nil, err
	}
	restore, err := applyFileRewindPlans(plans)
	if err != nil {
		return nil, nil, err
	}
	return results, restore, nil
}

func selectFileRewindPlans(
	candidates []fileRewindCandidate,
	forceKeys []string,
) ([]event.RewindFileResult, []fileRewindPlan, error) {
	force := make(map[string]struct{}, len(forceKeys))
	for _, key := range forceKeys {
		force[key] = struct{}{}
	}
	plans := make([]fileRewindPlan, 0, len(candidates))
	results := make([]event.RewindFileResult, 0, len(candidates))
	for _, candidate := range candidates {
		switch candidate.status {
		case RewindFileReady:
			plans = append(plans, candidate.safePlan)
			results = append(results, event.RewindFileResult{
				Path:   candidate.path,
				Action: event.RewindFileRestored,
			})
		case RewindFileMergeable:
			plans = append(plans, candidate.safePlan)
			results = append(results, event.RewindFileResult{
				Path:   candidate.path,
				Action: event.RewindFileMerged,
			})
		case RewindFileModified:
			if _, selected := force[candidate.key]; selected {
				delete(force, candidate.key)
				plans = append(plans, candidate.forcePlan)
				results = append(results, event.RewindFileResult{
					Path:   candidate.path,
					Action: event.RewindFileForceRestored,
				})
			} else {
				results = append(results, event.RewindFileResult{
					Path:   candidate.path,
					Action: event.RewindFileKept,
				})
			}
		default:
			return nil, nil, fmt.Errorf("%w: invalid file status", ErrFileRewindConflict)
		}
	}
	if len(force) > 0 {
		return nil, nil, fmt.Errorf("%w: unknown force file", ErrFileRewindConflict)
	}
	return results, plans, nil
}

func applyFileRewindPlans(plans []fileRewindPlan) (func() error, error) {
	applied := make([]fileRewindPlan, 0, len(plans))
	for _, plan := range plans {
		if err := verifyFileSnapshot(plan.path, plan.before); err != nil {
			_ = restoreFilePlans(applied)
			return nil, err
		}
		if err := writeFileSnapshot(plan.path, plan.after, plan.before.mode); err != nil {
			_ = restoreFilePlans(applied)
			return nil, fmt.Errorf("restore %s: %w", plan.path, err)
		}
		applied = append(applied, plan)
	}
	return func() error {
		return restoreFilePlans(applied)
	}, nil
}

func fileRewindBackups(plans []fileRewindPlan) []state.FileRewindBackup {
	backups := make([]state.FileRewindBackup, 0, len(plans))
	for _, plan := range plans {
		backup := state.FileRewindBackup{
			Path:          plan.path,
			BeforeExists:  plan.before.exists,
			BeforeMode:    uint32(plan.before.mode.Perm()),
			BeforeContent: append([]byte(nil), plan.before.content...),
			AfterExists:   plan.after.exists,
			AfterMode:     uint32(plan.after.mode.Perm()),
			AfterContent:  append([]byte(nil), plan.after.content...),
		}
		if backup.BeforeExists {
			backup.BeforeBlob = contentHash(backup.BeforeContent)
		}
		if backup.AfterExists {
			backup.AfterBlob = contentHash(backup.AfterContent)
		}
		backups = append(backups, backup)
	}
	return backups
}

func threeWayRestore(
	afterAgent fileSnapshot,
	current fileSnapshot,
	beforeAgent fileSnapshot,
) (fileSnapshot, bool) {
	if !afterAgent.exists || !current.exists || !beforeAgent.exists {
		return fileSnapshot{}, false
	}
	merged, ok := mergeFileLines(
		afterAgent.content,
		current.content,
		beforeAgent.content,
	)
	if !ok {
		return fileSnapshot{}, false
	}
	return fileSnapshot{
		exists:  true,
		content: merged,
		mode:    current.mode,
	}, true
}

func mergeFileLines(base, current, target []byte) ([]byte, bool) {
	baseLines := splitLinesWithEndings(base)
	currentLines := splitLinesWithEndings(current)
	targetLines := splitLinesWithEndings(target)
	userEdits, ok := calculateLineEdits(baseLines, currentLines)
	if !ok {
		return nil, false
	}
	rewindEdits, ok := calculateLineEdits(baseLines, targetLines)
	if !ok {
		return nil, false
	}

	mergedEdits := append([]lineEdit(nil), rewindEdits...)
	for _, userEdit := range userEdits {
		duplicate := false
		for _, rewindEdit := range rewindEdits {
			if equalLineEdits(userEdit, rewindEdit) {
				duplicate = true
				break
			}
			if lineEditsConflict(userEdit, rewindEdit) {
				return nil, false
			}
		}
		if !duplicate {
			mergedEdits = append(mergedEdits, userEdit)
		}
	}
	sort.Slice(mergedEdits, func(i, j int) bool {
		if mergedEdits[i].start != mergedEdits[j].start {
			return mergedEdits[i].start > mergedEdits[j].start
		}
		return mergedEdits[i].end > mergedEdits[j].end
	})
	lines := append([]string(nil), baseLines...)
	for _, edit := range mergedEdits {
		if edit.start < 0 || edit.end < edit.start || edit.end > len(lines) {
			return nil, false
		}
		next := make([]string, 0, len(lines)-(edit.end-edit.start)+len(edit.replacement))
		next = append(next, lines[:edit.start]...)
		next = append(next, edit.replacement...)
		next = append(next, lines[edit.end:]...)
		lines = next
	}
	return []byte(strings.Join(lines, "")), true
}

func calculateLineEdits(base, target []string) ([]lineEdit, bool) {
	if len(base) > 0 && len(target) > maxThreeWayMergeCells/len(base) {
		return nil, false
	}
	width := len(target) + 1
	lcs := make([]int, (len(base)+1)*width)
	at := func(i, j int) int {
		return i*width + j
	}
	for i := len(base) - 1; i >= 0; i-- {
		for j := len(target) - 1; j >= 0; j-- {
			if base[i] == target[j] {
				lcs[at(i, j)] = lcs[at(i+1, j+1)] + 1
			} else {
				lcs[at(i, j)] = max(lcs[at(i+1, j)], lcs[at(i, j+1)])
			}
		}
	}

	edits := make([]lineEdit, 0)
	baseIndex, targetIndex := 0, 0
	for baseIndex < len(base) || targetIndex < len(target) {
		if baseIndex < len(base) &&
			targetIndex < len(target) &&
			base[baseIndex] == target[targetIndex] {
			baseIndex++
			targetIndex++
			continue
		}
		edit := lineEdit{start: baseIndex}
		for baseIndex < len(base) || targetIndex < len(target) {
			if baseIndex < len(base) &&
				targetIndex < len(target) &&
				base[baseIndex] == target[targetIndex] {
				break
			}
			if targetIndex < len(target) &&
				(baseIndex == len(base) ||
					lcs[at(baseIndex, targetIndex+1)] >
						lcs[at(baseIndex+1, targetIndex)]) {
				edit.replacement = append(edit.replacement, target[targetIndex])
				targetIndex++
			} else {
				baseIndex++
			}
		}
		edit.end = baseIndex
		edits = append(edits, edit)
	}
	return edits, true
}

func equalLineEdits(left, right lineEdit) bool {
	return left.start == right.start &&
		left.end == right.end &&
		stringSlicesEqual(left.replacement, right.replacement)
}

func lineEditsConflict(left, right lineEdit) bool {
	leftInsert := left.start == left.end
	rightInsert := right.start == right.end
	switch {
	case leftInsert && rightInsert:
		return left.start == right.start
	case leftInsert:
		return left.start >= right.start && left.start <= right.end
	case rightInsert:
		return right.start >= left.start && right.start <= left.end
	default:
		return left.start < right.end && right.start < left.end
	}
}

func stringSlicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func splitLinesWithEndings(content []byte) []string {
	if len(content) == 0 {
		return nil
	}
	text := string(content)
	lines := make([]string, 0, bytes.Count(content, []byte{'\n'})+1)
	for len(text) > 0 {
		index := strings.IndexByte(text, '\n')
		if index < 0 {
			lines = append(lines, text)
			break
		}
		lines = append(lines, text[:index+1])
		text = text[index+1:]
	}
	return lines
}

func snapshotsEqual(left, right fileSnapshot) bool {
	return left.exists == right.exists &&
		(!left.exists ||
			left.mode.Perm() == right.mode.Perm() &&
				bytes.Equal(left.content, right.content))
}

func readFileSnapshot(path string) (fileSnapshot, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return fileSnapshot{}, nil
	}
	if err != nil {
		return fileSnapshot{}, fmt.Errorf("inspect %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fileSnapshot{}, fmt.Errorf("%w: %s is not a regular file", ErrFileRewindConflict, path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return fileSnapshot{}, fmt.Errorf("read %s: %w", path, err)
	}
	return fileSnapshot{
		exists:  true,
		content: content,
		mode:    info.Mode().Perm(),
	}, nil
}

func verifyFileSnapshot(path string, expected fileSnapshot) error {
	current, err := readFileSnapshot(path)
	if err != nil {
		return err
	}
	if !snapshotsEqual(current, expected) {
		return fmt.Errorf("%w: %s changed while preparing the rewind", ErrFileRewindConflict, path)
	}
	return nil
}

func writeFileSnapshot(path string, snapshot fileSnapshot, fallbackMode fs.FileMode) error {
	if !snapshot.exists {
		err := os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	mode := snapshot.mode.Perm()
	if mode == 0 {
		mode = fallbackMode.Perm()
	}
	if mode == 0 {
		mode = 0o644
	}
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".foya-rewind-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(snapshot.content); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func restoreFilePlans(plans []fileRewindPlan) error {
	var joined error
	for i := len(plans) - 1; i >= 0; i-- {
		plan := plans[i]
		if err := writeFileSnapshot(plan.path, plan.before, plan.before.mode); err != nil {
			joined = errors.Join(joined, fmt.Errorf("restore %s: %w", plan.path, err))
		}
	}
	return joined
}

// RecoverFileRewinds repairs filesystem state after a process stopped between
// applying file changes and committing the corresponding history event.
func RecoverFileRewinds(ctx context.Context, store state.Store) error {
	journals, err := store.PendingFileRewinds(ctx)
	if err != nil {
		return err
	}
	for _, journal := range journals {
		if journal.State == state.FileRewindPrepared {
			for _, file := range journal.Files {
				before, err := journalFileSnapshot(
					ctx,
					store,
					file.BeforeExists,
					file.BeforeMode,
					file.BeforeBlob,
				)
				if err != nil {
					return fmt.Errorf("load recovery before-state for %s: %w", file.Path, err)
				}
				after, err := journalFileSnapshot(
					ctx,
					store,
					file.AfterExists,
					file.AfterMode,
					file.AfterBlob,
				)
				if err != nil {
					return fmt.Errorf("load recovery after-state for %s: %w", file.Path, err)
				}
				current, err := readFileSnapshot(file.Path)
				if err != nil {
					return err
				}
				switch {
				case snapshotsEqual(current, before):
					continue
				case snapshotsEqual(current, after):
					if err := writeFileSnapshot(file.Path, before, current.mode); err != nil {
						return fmt.Errorf("recover file rewind %s: %w", journal.ID, err)
					}
				default:
					return fmt.Errorf(
						"%w: %s changed while recovering interrupted rewind",
						ErrFileRewindConflict,
						file.Path,
					)
				}
			}
		} else if journal.State != state.FileRewindCommitted {
			return fmt.Errorf("unknown file rewind journal state %q", journal.State)
		}
		if err := store.FinishFileRewind(ctx, journal.ID); err != nil {
			return err
		}
	}
	return nil
}

func journalFileSnapshot(
	ctx context.Context,
	store state.Store,
	exists bool,
	mode uint32,
	blob string,
) (fileSnapshot, error) {
	snapshot := fileSnapshot{
		exists: exists,
		mode:   fs.FileMode(mode).Perm(),
	}
	if !exists {
		return snapshot, nil
	}
	content, err := store.FileBlob(ctx, blob)
	if err != nil {
		return fileSnapshot{}, err
	}
	snapshot.content = content
	return snapshot, nil
}

func contentHash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
