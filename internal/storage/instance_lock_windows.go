//go:build windows

package storage

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

type InstanceLock struct {
	file       *os.File
	overlapped windows.Overlapped
}

func AcquireInstanceLock(dataDir string) (*InstanceLock, error) {
	file, err := openInstanceLockFile(dataDir)
	if err != nil {
		return nil, err
	}
	lock := &InstanceLock{file: file}
	err = windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		1,
		0,
		&lock.overlapped,
	)
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("%w: %s", ErrDataDirLocked, dataDir)
	}
	if err := writeInstanceOwner(file); err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("write instance lock: %w", err)
	}
	return lock, nil
}

func (lock *InstanceLock) Close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	unlockErr := windows.UnlockFileEx(
		windows.Handle(lock.file.Fd()),
		0,
		1,
		0,
		&lock.overlapped,
	)
	closeErr := lock.file.Close()
	lock.file = nil
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
