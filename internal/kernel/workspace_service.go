package kernel

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	conversation "github.com/freesoulcode/foya/internal/conversation"
)

const (
	WorkspaceKindProject = "project"
	WorkspaceKindManaged = "managed"

	maxWorkspaceEntries     = 10_000
	maxWorkspacePreviewSize = 2 << 20
)

var (
	ErrInvalidWorkspacePath = errors.New("invalid workspace path")
	ErrWorkspaceEntryExists = errors.New("workspace entry already exists")
)

var ignoredWorkspaceDirectories = map[string]struct{}{
	".git":         {},
	".idea":        {},
	".next":        {},
	".nuxt":        {},
	".turbo":       {},
	"coverage":     {},
	"dist":         {},
	"node_modules": {},
	"target":       {},
	"vendor":       {},
}

// SessionWorkspace describes the working directory backing one session.
// Project and managed workspaces expose the same capabilities to clients.
type SessionWorkspace struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	ProjectID string `json:"project_id,omitempty"`
}

type WorkspaceEntry struct {
	Path        string `json:"path"`
	Name        string `json:"name"`
	IsDirectory bool   `json:"is_dir"`
}

func (b *Service) SessionWorkspace(ctx context.Context, sessionID string) (SessionWorkspace, error) {
	session, ok := b.sessions.Get(sessionID)
	if !ok {
		return SessionWorkspace{}, conversation.ErrNotFound
	}
	if session.ProjectID != "" {
		project, err := b.Project(session.ProjectID)
		if err != nil {
			return SessionWorkspace{}, err
		}
		return SessionWorkspace{
			Kind:      WorkspaceKindProject,
			Name:      project.Name,
			Path:      project.Path,
			ProjectID: project.ID,
		}, nil
	}

	b.mu.RLock()
	store := b.artifacts
	b.mu.RUnlock()
	if store == nil {
		return SessionWorkspace{}, errors.New("artifact store is unavailable")
	}
	path, err := store.WorkspaceDir(ctx, sessionID)
	if err != nil {
		return SessionWorkspace{}, err
	}
	return SessionWorkspace{
		Kind: WorkspaceKindManaged,
		Name: "Workspace",
		Path: path,
	}, nil
}

func (b *Service) sessionWorkspacePath(ctx context.Context, sessionID string) (string, error) {
	workspace, err := b.SessionWorkspace(ctx, sessionID)
	if err != nil {
		return "", err
	}
	return workspace.Path, nil
}

func (b *Service) ListWorkspaceFiles(ctx context.Context, sessionID string) ([]WorkspaceEntry, error) {
	root, err := b.workspaceRoot(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	entries := make([]WorkspaceEntry, 0)
	if err := collectWorkspaceEntries(ctx, root, root, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func (b *Service) ReadWorkspaceFile(ctx context.Context, sessionID, relativePath string) (string, error) {
	root, err := b.workspaceRoot(ctx, sessionID)
	if err != nil {
		return "", err
	}
	file, err := existingWorkspaceEntry(root, relativePath)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(file)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("workspace entry is not a file")
	}
	if info.Size() > maxWorkspacePreviewSize {
		return "", errors.New("files larger than 2 MiB cannot be previewed")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(data) {
		return "", errors.New("binary file previews are not supported")
	}
	return string(data), nil
}

func (b *Service) CreateWorkspaceEntry(
	ctx context.Context,
	sessionID, relativePath string,
	directory bool,
) (string, error) {
	root, err := b.workspaceRoot(ctx, sessionID)
	if err != nil {
		return "", err
	}
	target, err := newWorkspaceEntry(root, relativePath)
	if err != nil {
		return "", err
	}
	if directory {
		err = os.Mkdir(target, 0o700)
	} else {
		var file *os.File
		file, err = os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			err = file.Close()
		}
	}
	if err != nil {
		return "", err
	}
	return workspaceRelative(root, target)
}

func (b *Service) RenameWorkspaceEntry(
	ctx context.Context,
	sessionID, relativePath, newName string,
) (string, error) {
	root, err := b.workspaceRoot(ctx, sessionID)
	if err != nil {
		return "", err
	}
	entry, err := existingWorkspaceEntry(root, relativePath)
	if err != nil {
		return "", err
	}
	if err := validWorkspaceName(newName); err != nil {
		return "", err
	}
	destination := filepath.Join(filepath.Dir(entry), newName)
	if _, err := os.Lstat(destination); err == nil {
		return "", ErrWorkspaceEntryExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.Rename(entry, destination); err != nil {
		return "", err
	}
	return workspaceRelative(root, destination)
}

func (b *Service) DeleteWorkspaceEntry(ctx context.Context, sessionID, relativePath string) error {
	root, err := b.workspaceRoot(ctx, sessionID)
	if err != nil {
		return err
	}
	entry, err := existingWorkspaceEntry(root, relativePath)
	if err != nil {
		return err
	}
	info, err := os.Lstat(entry)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return os.RemoveAll(entry)
	}
	return os.Remove(entry)
}

func (b *Service) ResolveWorkspacePath(
	ctx context.Context,
	sessionID, relativePath string,
) (string, error) {
	root, err := b.workspaceRoot(ctx, sessionID)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(relativePath) == "" {
		return root, nil
	}
	return existingWorkspaceEntry(root, relativePath)
}

func (b *Service) workspaceRoot(ctx context.Context, sessionID string) (string, error) {
	path, err := b.sessionWorkspacePath(ctx, sessionID)
	if err != nil {
		return "", err
	}
	root, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("workspace path is not a directory")
	}
	return root, nil
}

func workspaceRelativePath(relativePath string) (string, error) {
	if relativePath == "" || filepath.IsAbs(relativePath) {
		return "", ErrInvalidWorkspacePath
	}
	clean := filepath.Clean(filepath.FromSlash(relativePath))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", ErrInvalidWorkspacePath
	}
	return clean, nil
}

func existingWorkspaceEntry(root, relativePath string) (string, error) {
	relative, err := workspaceRelativePath(relativePath)
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(root, relative)
	info, err := os.Lstat(candidate)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("symbolic links are not supported")
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", err
	}
	if err := ensureWorkspaceChild(root, resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

func newWorkspaceEntry(root, relativePath string) (string, error) {
	relative, err := workspaceRelativePath(relativePath)
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(root, relative)
	parent, err := filepath.EvalSymlinks(filepath.Dir(candidate))
	if err != nil {
		return "", err
	}
	if err := ensureWorkspaceChild(root, parent); err != nil {
		return "", err
	}
	info, err := os.Stat(parent)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("workspace parent is not a directory")
	}
	if _, err := os.Lstat(candidate); err == nil {
		return "", ErrWorkspaceEntryExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return candidate, nil
}

func ensureWorkspaceChild(root, candidate string) error {
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return ErrInvalidWorkspacePath
	}
	return nil
}

func validWorkspaceName(name string) error {
	if strings.TrimSpace(name) == "" || name == "." || name == ".." ||
		strings.ContainsAny(name, `/\`) {
		return ErrInvalidWorkspacePath
	}
	return nil
}

func workspaceRelative(root, target string) (string, error) {
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return "", err
	}
	if err := ensureWorkspaceChild(root, target); err != nil {
		return "", err
	}
	return filepath.ToSlash(relative), nil
}

func collectWorkspaceEntries(
	ctx context.Context,
	root, directory string,
	entries *[]WorkspaceEntry,
) error {
	if len(*entries) >= maxWorkspaceEntries {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	children, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("read workspace directory: %w", err)
	}
	sort.Slice(children, func(i, j int) bool {
		return strings.ToLower(children[i].Name()) < strings.ToLower(children[j].Name())
	})
	for _, child := range children {
		if len(*entries) >= maxWorkspaceEntries {
			break
		}
		info, err := child.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if info.IsDir() {
			if _, ignored := ignoredWorkspaceDirectories[child.Name()]; ignored {
				continue
			}
		}
		path := filepath.Join(directory, child.Name())
		relative, err := workspaceRelative(root, path)
		if err != nil {
			return err
		}
		*entries = append(*entries, WorkspaceEntry{
			Path:        relative,
			Name:        child.Name(),
			IsDirectory: info.IsDir(),
		})
		if info.IsDir() {
			if err := collectWorkspaceEntries(ctx, root, path, entries); err != nil {
				return err
			}
		}
	}
	return nil
}
