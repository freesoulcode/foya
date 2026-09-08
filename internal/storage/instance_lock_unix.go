//go:build unix

package storage

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

type InstanceLock struct {
	file *os.File
}

func AcquireInstanceLock(dataDir string) (*InstanceLock, error) {
	file, err := openInstanceLockFile(dataDir)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("%w: %s", ErrDataDirLocked, dataDir)
	}
	if err := writeInstanceOwner(file); err != nil {
		_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
		_ = file.Close()
		return nil, fmt.Errorf("write instance lock: %w", err)
	}
	return &InstanceLock{file: file}, nil
}

func (lock *InstanceLock) Close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	unlockErr := unix.Flock(int(lock.file.Fd()), unix.LOCK_UN)
	closeErr := lock.file.Close()
	lock.file = nil
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
