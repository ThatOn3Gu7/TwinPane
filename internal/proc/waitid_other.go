//go:build !linux && !windows

package proc

// blockUntilWaitable is not available here (see Child.Wait).
func blockUntilWaitable(int) bool { return false }
