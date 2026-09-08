package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var ErrDataDirLocked = errors.New("Foya data directory is already in use")

func openInstanceLockFile(dataDir string) (*os.File, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	path := filepath.Join(dataDir, "kernel.lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open instance lock: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("protect instance lock: %w", err)
	}
	return file, nil
}

func writeInstanceOwner(file *os.File) error {
	if err := file.Truncate(0); err != nil {
		return err
	}
	if _, err := file.Seek(0, 0); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(file, "%d\n", os.Getpid()); err != nil {
		return err
	}
	return file.Sync()
}
