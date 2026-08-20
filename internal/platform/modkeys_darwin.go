package platform

/*
#cgo LDFLAGS: -framework CoreGraphics
#include <CoreGraphics/CoreGraphics.h>
*/
import "C"

// ShiftPollingSupported reports whether IsShiftPressed can detect anything,
// so callers can skip polling it entirely.
const ShiftPollingSupported = true

// IsShiftPressed polls the OS-level modifier key state via CoreGraphics.
// Returns true if either Shift key is currently held down.
// This works without any special permissions on macOS.
func IsShiftPressed() bool {
	flags := C.CGEventSourceFlagsState(C.kCGEventSourceStateCombinedSessionState)
	return flags&C.kCGEventFlagMaskShift != 0
}
