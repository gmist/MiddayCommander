//go:build !darwin && !windows

package platform

// ShiftPollingSupported reports whether IsShiftPressed can detect anything,
// so callers can skip polling it entirely.
const ShiftPollingSupported = false

// IsShiftPressed is a no-op on Linux and other platforms.
// Shift detection there relies on the Kitty keyboard protocol instead.
func IsShiftPressed() bool {
	return false
}
