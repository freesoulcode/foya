//go:build !windows

package main

import (
	"net"
	"os"
	"path/filepath"
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
