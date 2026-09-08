package storage

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestInstanceLockExcludesSecondKernel(t *testing.T) {
	dataDir := t.TempDir()
	first, err := AcquireInstanceLock(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()

	if second, err := AcquireInstanceLock(dataDir); !errors.Is(err, ErrDataDirLocked) {
		if second != nil {
			_ = second.Close()
		}
		t.Fatalf("second lock error = %v, want ErrDataDirLocked", err)
	}

	data, err := os.ReadFile(filepath.Join(dataDir, "kernel.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != strconv.Itoa(os.Getpid()) {
		t.Fatalf("lock owner = %q", data)
	}

	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := AcquireInstanceLock(dataDir)
	if err != nil {
		t.Fatalf("lock was not released: %v", err)
	}
	_ = second.Close()
}
