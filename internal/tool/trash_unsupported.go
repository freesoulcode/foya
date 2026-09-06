//go:build !darwin && !linux && !windows

package tool

import "errors"

func moveToTrash(string) error {
	return errors.New("system trash is not supported on this platform")
}
