//go:build !windows

package main

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestListenUnixDoesNotReplaceActiveListener(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "foya-listen-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "kernel.sock")
	listener, _, err := listenUnix(path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	if second, _, err := listenUnix(path); err == nil {
		second.Close()
		t.Fatal("second listener unexpectedly replaced the active socket")
	}

	connection, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		t.Fatalf("original listener is no longer reachable: %v", err)
	}
	connection.Close()
}

func TestProcessAlive(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Fatal("current process reported as stopped")
	}
	if processAlive(1 << 30) {
		t.Fatal("nonexistent process reported as alive")
	}
}

func TestWatchParentProcessDisabledWithoutPID(t *testing.T) {
	t.Setenv(parentPIDEnv, "")
	if done := watchParentProcess(); done != nil {
		t.Fatal("watcher enabled without a parent PID")
	}
}

func TestWatchParentProcessDetectsExit(t *testing.T) {
	parent := exec.Command("sh", "-c", "exit 0")
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	pid := parent.Process.Pid
	if err := parent.Wait(); err != nil {
		t.Fatal(err)
	}
	t.Setenv(parentPIDEnv, strconv.Itoa(pid))
	done := watchParentProcess()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("parent exit was not detected")
	}
}
