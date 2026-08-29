//go:build windows

package terminal

import "context"

type unavailableManager struct{}

// NewManager returns a manager that reports the unavailable platform capability.
func NewManager() Manager {
	return unavailableManager{}
}

func (unavailableManager) Start(context.Context, string, string, uint16, uint16) (Snapshot, error) {
	return Snapshot{}, ErrUnavailable
}

func (unavailableManager) Attach(string, string) (Snapshot, error) {
	return Snapshot{}, ErrUnavailable
}

func (unavailableManager) Write(string, string, string) error {
	return ErrUnavailable
}

func (unavailableManager) Resize(string, string, uint16, uint16) error {
	return ErrUnavailable
}

func (unavailableManager) Stop(string, string) error {
	return ErrUnavailable
}

func (unavailableManager) Subscribe(context.Context, string, string, uint64) (<-chan DataEvent, error) {
	return nil, ErrUnavailable
}

func (unavailableManager) CloseSession(string) {}
