//go:build linux

package sandbox

import (
	"fmt"
	"os"
)

const bubblewrapExecutable = "/usr/bin/bwrap"

type bubblewrapSandbox struct{}

func newPlatformSandbox() Sandbox {
	return bubblewrapSandbox{}
}

func (bubblewrapSandbox) Kind() Kind {
	return KindLinuxBubblewrap
}

func (bubblewrapSandbox) Wrap(req ExecRequest, profile Profile) (ExecRequest, error) {
	if profile.FileSystem == FSFull {
		return req, nil
	}
	if _, err := os.Stat(bubblewrapExecutable); err != nil {
		return ExecRequest{}, fmt.Errorf("%w: %s: %v", ErrUnavailable, bubblewrapExecutable, err)
	}
	profile.WritableRoots = existingRoots(profile.WritableRoots)
	profile.ReadOnlyRoots = existingRoots(profile.ReadOnlyRoots)
	return bubblewrapRequest([]string{bubblewrapExecutable}, nil, req, profile)
}
