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
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
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

type ProcessSnapshot struct {
	PID      int
	Running  bool
	ExitCode int
	Stdout   []byte
	Stderr   []byte
}

// Process is a running sandboxed command. It can outlive the context which
// initiated a tool call when explicitly started as a background command.
type Process interface {
	Snapshot() ProcessSnapshot
	Done() <-chan struct{}
	Wait() (ExecResult, error)
	Stop() error
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

// ManagedRunner additionally supports commands whose lifecycle is owned by
// the kernel rather than one synchronous tool invocation.
type ManagedRunner interface {
	Runner
	Start(ctx context.Context, req ExecRequest, profile Profile) (Process, error)
}

type runner struct {
	backend Sandbox
}

type runningProcess struct {
	mu       sync.RWMutex
	cmd      *exec.Cmd
	stdout   *lockedBuffer
	stderr   *lockedBuffer
	running  bool
	exitCode int
	err      error
	done     chan struct{}
}

type lockedBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *lockedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	const maxProcessOutputBytes = 1 << 20
	size := len(data)
	if size >= maxProcessOutputBytes {
		b.Buffer.Reset()
		_, _ = b.Buffer.Write(data[size-maxProcessOutputBytes:])
		return size, nil
	}
	_, _ = b.Buffer.Write(data)
	if b.Buffer.Len() > maxProcessOutputBytes {
		current := append([]byte(nil), b.Buffer.Bytes()...)
		b.Buffer.Reset()
		_, _ = b.Buffer.Write(current[len(current)-maxProcessOutputBytes:])
	}
	return size, nil
}

func (b *lockedBuffer) ReadFrom(reader io.Reader) (int64, error) {
	buffer := make([]byte, 32*1024)
	var total int64
	for {
		count, err := reader.Read(buffer)
		if count > 0 {
			written, writeErr := b.Write(buffer[:count])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
		}
		if errors.Is(err, io.EOF) {
			return total, nil
		}
		if err != nil {
			return total, err
		}
	}
}

func (b *lockedBuffer) BytesCopy() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.Buffer.Bytes()...)
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
	process, err := r.Start(ctx, req, profile)
	if err != nil {
		return ExecResult{}, err
	}
	return process.Wait()
}

func (r *runner) Start(
	ctx context.Context,
	req ExecRequest,
	profile Profile,
) (Process, error) {
	if len(req.Argv) == 0 {
		return nil, errors.New("sandbox command is empty")
	}
	wrapped, err := r.backend.Wrap(req, profile)
	if err != nil {
		return nil, err
	}
	if len(wrapped.Argv) == 0 {
		return nil, errors.New("sandbox returned an empty command")
	}
	cmd := exec.CommandContext(ctx, wrapped.Argv[0], wrapped.Argv[1:]...)
	cmd.Dir = wrapped.Dir
	cmd.Env = wrapped.Env
	cmd.Stdin = bytes.NewReader(wrapped.Stdin)
	stdout := &lockedBuffer{}
	stderr := &lockedBuffer{}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	configureProcessTree(cmd)
	process := &runningProcess{
		cmd:      cmd,
		stdout:   stdout,
		stderr:   stderr,
		running:  true,
		exitCode: -1,
		done:     make(chan struct{}),
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go process.reap()
	return process, nil
}

func (p *runningProcess) reap() {
	err := p.cmd.Wait()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}
	p.mu.Lock()
	p.running = false
	p.exitCode = exitCode
	p.err = err
	p.mu.Unlock()
	close(p.done)
}

func (p *runningProcess) Snapshot() ProcessSnapshot {
	p.mu.RLock()
	running := p.running
	exitCode := p.exitCode
	pid := 0
	if p.cmd.Process != nil {
		pid = p.cmd.Process.Pid
	}
	p.mu.RUnlock()
	return ProcessSnapshot{
		PID:      pid,
		Running:  running,
		ExitCode: exitCode,
		Stdout:   p.stdout.BytesCopy(),
		Stderr:   p.stderr.BytesCopy(),
	}
}

func (p *runningProcess) Done() <-chan struct{} {
	return p.done
}

func (p *runningProcess) Wait() (ExecResult, error) {
	<-p.done
	p.mu.RLock()
	err := p.err
	p.mu.RUnlock()
	return ExecResult{
		Stdout: p.stdout.BytesCopy(),
		Stderr: p.stderr.BytesCopy(),
	}, err
}

func (p *runningProcess) Stop() error {
	p.mu.RLock()
	running := p.running
	process := p.cmd.Process
	p.mu.RUnlock()
	if !running || process == nil {
		return nil
	}
	return stopProcessTree(process)
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
