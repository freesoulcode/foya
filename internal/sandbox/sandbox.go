// Package sandbox 约束工具执行的副作用爆炸半径。
//
// 它在 macOS 上使用 Seatbelt、在 Linux 上使用 Bubblewrap、在 Windows
// 上通过 WSL2 使用 Bubblewrap。沙箱是能力的约束层,不是能力本身。
package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Kind 标识沙箱后端。
type Kind string

const (
	KindNone            Kind = "none"
	KindMacSeatbelt     Kind = "mac_seatbelt"
	KindLinuxBubblewrap Kind = "linux_bubblewrap"
	KindWindowsWSL      Kind = "windows_wsl2_bubblewrap"
)

var ErrUnavailable = errors.New("sandbox is unavailable")

// FSAccess 是文件系统访问级别。
type FSAccess string

const (
	FSReadOnly     FSAccess = "read"
	FSProjectWrite FSAccess = "project_write"
	FSFull         FSAccess = "full"
)

// Profile 描述一次执行的隔离约束。
type Profile struct {
	FileSystem    FSAccess
	WritableRoots []string
	ReadOnlyRoots []string
	Network       bool
}

// ExecRequest 是待执行的命令。
type ExecRequest struct {
	Argv  []string
	Dir   string
	Env   []string
	Stdin []byte
	// PathArgs identifies argv indexes containing host paths. A VM or
	// compatibility-layer backend may translate only these trusted arguments.
	PathArgs []int
}

type ExecResult struct {
	Stdout []byte
	Stderr []byte
}

// Sandbox 把原始命令改写成沙箱包装后的命令。
type Sandbox interface {
	Wrap(req ExecRequest, profile Profile) (ExecRequest, error)
	Kind() Kind
}

// Runner executes every process through the selected platform boundary.
type Runner interface {
	Run(ctx context.Context, req ExecRequest, profile Profile) (ExecResult, error)
	Kind() Kind
}

type runner struct {
	backend Sandbox
}

func NewRunner() Runner {
	return &runner{backend: newPlatformSandbox()}
}

func NewRunnerWithBackend(backend Sandbox) Runner {
	return &runner{backend: backend}
}

func (r *runner) Kind() Kind {
	return r.backend.Kind()
}

func (r *runner) Run(
	ctx context.Context,
	req ExecRequest,
	profile Profile,
) (ExecResult, error) {
	if len(req.Argv) == 0 {
		return ExecResult{}, errors.New("sandbox command is empty")
	}
	wrapped, err := r.backend.Wrap(req, profile)
	if err != nil {
		return ExecResult{}, err
	}
	if len(wrapped.Argv) == 0 {
		return ExecResult{}, errors.New("sandbox returned an empty command")
	}
	cmd := exec.CommandContext(ctx, wrapped.Argv[0], wrapped.Argv[1:]...)
	cmd.Dir = wrapped.Dir
	cmd.Env = wrapped.Env
	cmd.Stdin = bytes.NewReader(wrapped.Stdin)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	return ExecResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}, err
}

// WorkspaceWriteProfile permits writes only inside the project and temporary
// roots. Agent metadata remains read-only even though it is inside the project.
func WorkspaceWriteProfile(projectDir string) Profile {
	writable := []string{os.TempDir()}
	var readOnly []string
	if projectDir != "" {
		writable = append(writable, projectDir)
		for _, name := range []string{".git", ".agents", ".foya"} {
			readOnly = append(readOnly, filepath.Join(projectDir, name))
		}
	}
	if cacheDir, err := os.UserCacheDir(); err == nil {
		writable = append(writable, cacheDir)
	}
	if homeDir, err := os.UserHomeDir(); err == nil {
		writable = append(writable, filepath.Join(homeDir, ".cache"))
	}
	return Profile{
		FileSystem:    FSProjectWrite,
		WritableRoots: cleanRoots(writable),
		ReadOnlyRoots: cleanRoots(readOnly),
	}
}

func cleanRoots(roots []string) []string {
	seen := make(map[string]bool)
	cleaned := make([]string, 0, len(roots)*2)
	for _, root := range roots {
		if root == "" {
			continue
		}
		absolute, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		for _, candidate := range []string{filepath.Clean(absolute), evaluatedPath(absolute)} {
			if candidate != "" && !seen[candidate] {
				seen[candidate] = true
				cleaned = append(cleaned, candidate)
			}
		}
	}
	return cleaned
}

func evaluatedPath(path string) string {
	evaluated, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ""
	}
	return filepath.Clean(evaluated)
}

type unavailableSandbox struct{}

func (unavailableSandbox) Kind() Kind { return KindNone }

func (unavailableSandbox) Wrap(req ExecRequest, profile Profile) (ExecRequest, error) {
	if profile.FileSystem == FSFull {
		return req, nil
	}
	return ExecRequest{}, fmt.Errorf("%w on this platform", ErrUnavailable)
}
