package sandbox

import (
	"errors"
	"fmt"
	"os"
)

const wslBubblewrapExecutable = "/usr/bin/bwrap"

func bubblewrapRequest(
	launcher []string,
	hardening []string,
	req ExecRequest,
	profile Profile,
) (ExecRequest, error) {
	if profile.FileSystem != FSReadOnly && profile.FileSystem != FSProjectWrite {
		return ExecRequest{}, fmt.Errorf("unsupported filesystem profile %q", profile.FileSystem)
	}
	if len(launcher) == 0 {
		return ExecRequest{}, errors.New("bubblewrap launcher is empty")
	}

	argv := append([]string(nil), launcher...)
	argv = append(argv,
		"--die-with-parent",
		"--new-session",
		"--unshare-pid",
		"--unshare-ipc",
		"--unshare-uts",
		"--unshare-cgroup-try",
		"--cap-drop", "ALL",
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--proc", "/proc",
	)
	argv = append(argv, hardening...)
	if !profile.Network {
		argv = append(argv, "--unshare-net", "--tmpfs", "/run")
	}
	if profile.FileSystem == FSProjectWrite {
		for _, root := range profile.WritableRoots {
			argv = append(argv, "--bind", root, root)
		}
		for _, root := range profile.ReadOnlyRoots {
			argv = append(argv, "--ro-bind", root, root)
		}
	}
	if req.Dir != "" {
		argv = append(argv, "--chdir", req.Dir)
	}
	argv = append(argv, "--")
	argv = append(argv, req.Argv...)

	req.Argv = argv
	req.Dir = ""
	req.PathArgs = nil
	return req, nil
}

func existingRoots(roots []string) []string {
	existing := make([]string, 0, len(roots))
	for _, root := range cleanRoots(roots) {
		if _, err := os.Stat(root); err == nil {
			existing = append(existing, root)
		}
	}
	return existing
}

func wslBubblewrapLauncher(executable string) []string {
	return []string{
		executable,
		"--exec",
		wslBubblewrapExecutable,
	}
}

func wslBubblewrapHardening() []string {
	return []string{
		"--unsetenv", "WSL_INTEROP",
		"--unsetenv", "WSLENV",
		"--ro-bind", "/dev/null", "/init",
	}
}
