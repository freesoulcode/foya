//go:build windows

package sandbox

import (
	"errors"
	"os"
	"os/exec"
	"time"
)

func configureProcessTree(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return stopProcessTree(cmd.Process)
	}
	cmd.WaitDelay = 2 * time.Second
}

func stopProcessTree(process *os.Process) error {
	err := process.Kill()
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}
