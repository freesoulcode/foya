//go:build windows

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

type wslSandbox struct {
	executable string
	checkOnce  sync.Once
	checkErr   error
}

func newPlatformSandbox() Sandbox {
	systemRoot := os.Getenv("SystemRoot")
	if systemRoot == "" {
		systemRoot = `C:\Windows`
	}
	return &wslSandbox{
		executable: filepath.Join(systemRoot, "System32", "wsl.exe"),
	}
}

func (*wslSandbox) Kind() Kind {
	return KindWindowsWSL
}

func (s *wslSandbox) Wrap(req ExecRequest, profile Profile) (ExecRequest, error) {
	if profile.FileSystem == FSFull {
		return req, nil
	}
	if err := s.available(); err != nil {
		return ExecRequest{}, err
	}

	translated, err := s.translateRequest(req)
	if err != nil {
		return ExecRequest{}, err
	}
	translatedProfile, err := s.translateProfile(profile)
	if err != nil {
		return ExecRequest{}, err
	}
	return bubblewrapRequest(
		wslBubblewrapLauncher(s.executable),
		wslBubblewrapHardening(),
		translated,
		translatedProfile,
	)
}

func (s *wslSandbox) available() error {
	s.checkOnce.Do(func() {
		if _, err := os.Stat(s.executable); err != nil {
			s.checkErr = fmt.Errorf("%w: %s: %v", ErrUnavailable, s.executable, err)
			return
		}
		check := exec.Command(
			s.executable,
			"--exec",
			"/bin/sh",
			"-c",
			`case "$(uname -r)" in *microsoft-standard-WSL2*|*WSL2*) test -x /usr/bin/bwrap;; *) exit 1;; esac`,
		)
		if output, err := check.CombinedOutput(); err != nil {
			detail := strings.TrimSpace(string(output))
			if detail != "" {
				detail = ": " + detail
			}
			s.checkErr = fmt.Errorf(
				"%w: WSL2 with %s is required%s",
				ErrUnavailable,
				wslBubblewrapExecutable,
				detail,
			)
		}
	})
	return s.checkErr
}

func (s *wslSandbox) translateRequest(req ExecRequest) (ExecRequest, error) {
	translated := req
	translated.Argv = append([]string(nil), req.Argv...)

	if isWindowsCommandShell(translated.Argv) {
		translated.Argv = []string{"/bin/sh", "-c", translated.Argv[2]}
		translated.PathArgs = nil
	} else {
		for _, index := range translated.PathArgs {
			if index < 0 || index >= len(translated.Argv) {
				return ExecRequest{}, fmt.Errorf("invalid path argument index %d", index)
			}
			path, err := s.translatePath(translated.Argv[index])
			if err != nil {
				return ExecRequest{}, err
			}
			translated.Argv[index] = path
		}
	}

	if translated.Dir != "" {
		path, err := s.translatePath(translated.Dir)
		if err != nil {
			return ExecRequest{}, err
		}
		translated.Dir = path
	}
	return translated, nil
}

func (s *wslSandbox) translateProfile(profile Profile) (Profile, error) {
	var err error
	profile.WritableRoots, err = s.translateExistingRoots(profile.WritableRoots)
	if err != nil {
		return Profile{}, err
	}
	profile.ReadOnlyRoots, err = s.translateExistingRoots(profile.ReadOnlyRoots)
	if err != nil {
		return Profile{}, err
	}
	return profile, nil
}

func (s *wslSandbox) translateExistingRoots(roots []string) ([]string, error) {
	var translated []string
	for _, root := range cleanRoots(roots) {
		if _, err := os.Stat(root); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("inspect sandbox root %q: %w", root, err)
		}
		path, err := s.translatePath(root)
		if err != nil {
			return nil, err
		}
		translated = append(translated, path)
	}
	return translated, nil
}

func (s *wslSandbox) translatePath(path string) (string, error) {
	output, err := exec.Command(
		s.executable,
		"--exec",
		"/usr/bin/wslpath",
		"-a",
		"-u",
		path,
	).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf(
			"translate Windows path %q through WSL: %w: %s",
			path,
			err,
			strings.TrimSpace(string(output)),
		)
	}
	translated := strings.TrimSpace(string(output))
	if translated == "" || !strings.HasPrefix(translated, "/") {
		return "", fmt.Errorf("translate Windows path %q through WSL: invalid result %q", path, translated)
	}
	return translated, nil
}

func isWindowsCommandShell(argv []string) bool {
	if len(argv) != 3 || !strings.EqualFold(argv[1], "/c") {
		return false
	}
	name := strings.ToLower(filepath.Base(argv[0]))
	return name == "cmd" || name == "cmd.exe"
}
