//go:build darwin

package sandbox

import (
	"fmt"
	"os"
)

const seatbeltExecutable = "/usr/bin/sandbox-exec"

type seatbeltSandbox struct{}

func newPlatformSandbox() Sandbox {
	return seatbeltSandbox{}
}

func (seatbeltSandbox) Kind() Kind {
	return KindMacSeatbelt
}

func (seatbeltSandbox) Wrap(req ExecRequest, profile Profile) (ExecRequest, error) {
	if profile.FileSystem == FSFull {
		return req, nil
	}
	if _, err := os.Stat(seatbeltExecutable); err != nil {
		return ExecRequest{}, fmt.Errorf("%w: %s: %v", ErrUnavailable, seatbeltExecutable, err)
	}
	policy, definitions, err := seatbeltPolicy(profile)
	if err != nil {
		return ExecRequest{}, err
	}
	argv := []string{seatbeltExecutable, "-p", policy}
	for _, root := range definitions {
		argv = append(argv, fmt.Sprintf("-D%s=%s", root.name, root.path))
	}
	argv = append(argv, "--")
	argv = append(argv, req.Argv...)
	req.Argv = argv
	return req, nil
}

type seatbeltDefinition struct {
	name string
	path string
}

func seatbeltPolicy(profile Profile) (string, []seatbeltDefinition, error) {
	if profile.FileSystem != FSReadOnly && profile.FileSystem != FSProjectWrite {
		return "", nil, fmt.Errorf("unsupported filesystem profile %q", profile.FileSystem)
	}
	policy := `(version 1)
(deny default)
(allow process*)
(allow signal (target same-sandbox))
(allow sysctl*)
(allow mach-lookup)
(allow ipc-posix*)
(allow file-read*)
(allow file-read* file-write-data (literal "/dev/null"))
(allow file-read* file-write* (regex #"^/dev/fd/[0-9]+$"))
`
	var definitions []seatbeltDefinition
	if profile.FileSystem == FSProjectWrite {
		for index, root := range cleanRoots(profile.WritableRoots) {
			name := fmt.Sprintf("WRITABLE_ROOT_%d", index)
			definitions = append(definitions, seatbeltDefinition{name: name, path: root})
			policy += fmt.Sprintf("(allow file-write* (subpath (param %q)))\n", name)
		}
		for index, root := range cleanRoots(profile.ReadOnlyRoots) {
			name := fmt.Sprintf("READ_ONLY_ROOT_%d", index)
			definitions = append(definitions, seatbeltDefinition{name: name, path: root})
			policy += fmt.Sprintf("(deny file-write* (subpath (param %q)))\n", name)
		}
	}
	if profile.Network {
		policy += "(allow network*)\n"
	}
	return policy, definitions, nil
}
