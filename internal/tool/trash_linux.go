//go:build linux

package tool

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func moveToTrash(path string) error {
	root, err := linuxTrashRoot()
	if err != nil {
		return err
	}
	filesDir := filepath.Join(root, "files")
	infoDir := filepath.Join(root, "info")
	if err := os.MkdirAll(filesDir, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(infoDir, 0o700); err != nil {
		return err
	}
	name, err := availableTrashName(filesDir, infoDir, filepath.Base(path))
	if err != nil {
		return err
	}
	infoPath := filepath.Join(infoDir, name+".trashinfo")
	content := fmt.Sprintf(
		"[Trash Info]\nPath=%s\nDeletionDate=%s\n",
		(&url.URL{Path: path}).EscapedPath(),
		time.Now().Format("2006-01-02T15:04:05"),
	)
	file, err := os.OpenFile(infoPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		_ = os.Remove(infoPath)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(infoPath)
		return err
	}
	if err := os.Rename(path, filepath.Join(filesDir, name)); err != nil {
		_ = os.Remove(infoPath)
		if errors.Is(err, syscall.EXDEV) {
			return errors.New("cannot move this path to the home trash across filesystems")
		}
		return err
	}
	return nil
}

func linuxTrashRoot() (string, error) {
	if value := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); filepath.IsAbs(value) {
		return filepath.Join(value, "Trash"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "Trash"), nil
}

func availableTrashName(filesDir, infoDir, base string) (string, error) {
	for suffix := 1; ; suffix++ {
		name := base
		if suffix > 1 {
			name = fmt.Sprintf("%s.%d", base, suffix)
		}
		_, fileErr := os.Lstat(filepath.Join(filesDir, name))
		_, infoErr := os.Lstat(filepath.Join(infoDir, name+".trashinfo"))
		if errors.Is(fileErr, os.ErrNotExist) && errors.Is(infoErr, os.ErrNotExist) {
			return name, nil
		}
		if fileErr != nil && !errors.Is(fileErr, os.ErrNotExist) {
			return "", fileErr
		}
		if infoErr != nil && !errors.Is(infoErr, os.ErrNotExist) {
			return "", infoErr
		}
	}
}
