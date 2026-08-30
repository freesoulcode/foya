//go:build !windows

package terminal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"

	"github.com/creack/pty"
	"github.com/freesoulcode/foya/internal/broker"
)

const (
	maxBufferBytes  = 1024 * 1024
	maxReplayEvents = 2048
)

type process struct {
	mu        sync.RWMutex
	ref       string
	sessionID string
	ptmx      *os.File
	cmd       *exec.Cmd
	running   bool
	exitCode  int
	buffer    []byte
	seq       uint64
	events    []DataEvent
}

type manager struct {
	mu        sync.RWMutex
	processes map[string]*process
	bus       *broker.Broker[DataEvent]
}

// NewManager creates an in-memory terminal resource manager.
func NewManager() Manager {
	return &manager{
		processes: make(map[string]*process),
		bus:       broker.New[DataEvent](),
	}
}

func (m *manager) Start(
	_ context.Context,
	sessionID, cwd string,
	cols, rows uint16,
) (Snapshot, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.Command(shell)
	if cwd != "" {
		cmd.Dir = cwd
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return Snapshot{}, err
		}
		cmd.Dir = home
	}
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		return Snapshot{}, err
	}

	ref := newRef()
	item := &process{
		ref:       ref,
		sessionID: sessionID,
		ptmx:      ptmx,
		cmd:       cmd,
		running:   true,
	}
	m.mu.Lock()
	m.processes[ref] = item
	m.mu.Unlock()

	go m.consume(item)
	return item.snapshot(), nil
}

func (m *manager) consume(item *process) {
	buf := make([]byte, 4096)
	for {
		n, err := item.ptmx.Read(buf)
		if n > 0 {
			m.append(item, string(buf[:n]), false, 0)
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && item.runningState() {
				m.append(item, "\r\n[终端连接已结束]\r\n", false, 0)
			}
			break
		}
	}

	waitErr := item.cmd.Wait()
	exitCode := 0
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}
	item.mu.Lock()
	item.running = false
	item.exitCode = exitCode
	item.mu.Unlock()
	m.append(item, "", true, exitCode)
	_ = item.ptmx.Close()
}

func (m *manager) append(item *process, data string, exited bool, exitCode int) {
	item.mu.Lock()
	item.seq++
	ev := DataEvent{
		SessionID: item.sessionID,
		Ref:       item.ref,
		Seq:       item.seq,
		Data:      data,
		Exited:    exited,
		ExitCode:  exitCode,
	}
	if data != "" {
		item.buffer = append(item.buffer, data...)
		if len(item.buffer) > maxBufferBytes {
			item.buffer = append([]byte(nil), item.buffer[len(item.buffer)-maxBufferBytes:]...)
		}
	}
	item.events = append(item.events, ev)
	if len(item.events) > maxReplayEvents {
		item.events = append([]DataEvent(nil), item.events[len(item.events)-maxReplayEvents:]...)
	}
	item.mu.Unlock()
	m.bus.Publish(topic(item.sessionID, item.ref), ev)
}

func (m *manager) Attach(sessionID, ref string) (Snapshot, error) {
	item, err := m.get(sessionID, ref)
	if err != nil {
		return Snapshot{}, err
	}
	return item.snapshot(), nil
}

func (m *manager) Write(sessionID, ref, input string) error {
	item, err := m.get(sessionID, ref)
	if err != nil {
		return err
	}
	item.mu.RLock()
	running := item.running
	ptmx := item.ptmx
	item.mu.RUnlock()
	if !running {
		return ErrNotFound
	}
	_, err = io.WriteString(ptmx, input)
	return err
}

func (m *manager) Resize(sessionID, ref string, cols, rows uint16) error {
	item, err := m.get(sessionID, ref)
	if err != nil {
		return err
	}
	if cols == 0 || rows == 0 {
		return nil
	}
	return pty.Setsize(item.ptmx, &pty.Winsize{Cols: cols, Rows: rows})
}

func (m *manager) Stop(sessionID, ref string) error {
	item, err := m.get(sessionID, ref)
	if err != nil {
		return err
	}
	item.mu.RLock()
	running := item.running
	process := item.cmd.Process
	item.mu.RUnlock()
	if !running || process == nil {
		return nil
	}
	return process.Kill()
}

func (m *manager) Subscribe(
	ctx context.Context,
	sessionID, ref string,
	after uint64,
) (<-chan DataEvent, error) {
	item, err := m.get(sessionID, ref)
	if err != nil {
		return nil, err
	}
	live := m.bus.Subscribe(ctx, topic(sessionID, ref))
	item.mu.RLock()
	replay := make([]DataEvent, 0, len(item.events))
	for _, ev := range item.events {
		if ev.Seq > after {
			replay = append(replay, ev)
		}
	}
	item.mu.RUnlock()

	out := make(chan DataEvent, 256)
	go func() {
		defer close(out)
		last := after
		for _, ev := range replay {
			select {
			case out <- ev:
				last = ev.Seq
			case <-ctx.Done():
				return
			}
		}
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-live:
				if !ok {
					return
				}
				if ev.Seq <= last {
					continue
				}
				select {
				case out <- ev:
					last = ev.Seq
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

func (m *manager) CloseSession(sessionID string) {
	m.mu.RLock()
	var refs []string
	for ref, item := range m.processes {
		if item.sessionID == sessionID {
			refs = append(refs, ref)
		}
	}
	m.mu.RUnlock()
	for _, ref := range refs {
		_ = m.Stop(sessionID, ref)
		m.mu.Lock()
		delete(m.processes, ref)
		m.mu.Unlock()
	}
}

func (m *manager) get(sessionID, ref string) (*process, error) {
	m.mu.RLock()
	item := m.processes[ref]
	m.mu.RUnlock()
	if item == nil || item.sessionID != sessionID {
		return nil, ErrNotFound
	}
	return item, nil
}

func (p *process) snapshot() Snapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return Snapshot{
		Ref:       p.ref,
		SessionID: p.sessionID,
		Running:   p.running,
		ExitCode:  p.exitCode,
		Buffer:    string(p.buffer),
		Seq:       p.seq,
	}
}

func (p *process) runningState() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.running
}

func topic(sessionID, ref string) string {
	return "terminal:" + sessionID + ":" + ref
}

func newRef() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
