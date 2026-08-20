//go:build darwin && !cgo

package platform

// ShiftPollingSupported reports whether IsShiftPressed can detect anything,
// so callers can skip polling it entirely.
const ShiftPollingSupported = false

// IsShiftPressed is a no-op when CGO is disabled (e.g. release builds).
// Shift detection relies on the Kitty keyboard protocol instead.
func IsShiftPressed() bool {
	return false
}
