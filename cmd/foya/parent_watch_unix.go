//go:build unix

package main

import (
	"errors"
	"os"
	"strconv"
	"syscall"
	"time"
)

const parentPIDEnv = "FOYA_PARENT_PID"

func watchParentProcess() <-chan struct{} {
	pid, err := strconv.Atoi(os.Getenv(parentPIDEnv))
	if err != nil || pid <= 1 {
		return nil
	}
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for range ticker.C {
			if !processAlive(pid) {
				close(done)
				return
			}
		}
	}()
	return done
}

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
