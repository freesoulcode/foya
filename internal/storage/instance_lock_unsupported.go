//go:build !unix && !windows

package storage

import "fmt"

type InstanceLock struct{}

func AcquireInstanceLock(dataDir string) (*InstanceLock, error) {
	return nil, fmt.Errorf("instance locking is unsupported on this platform: %s", dataDir)
}

func (lock *InstanceLock) Close() error {
	return nil
}
