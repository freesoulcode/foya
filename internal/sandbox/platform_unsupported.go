//go:build !darwin && !linux && !windows

package sandbox

func newPlatformSandbox() Sandbox {
	return unavailableSandbox{}
}
