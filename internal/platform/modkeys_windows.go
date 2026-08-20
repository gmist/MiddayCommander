//go:build windows

package platform

import "syscall"

var (
	user32           = syscall.NewLazyDLL("user32.dll")
	getAsyncKeyState = user32.NewProc("GetAsyncKeyState")
)

const vkShift = 0x10

// ShiftPollingSupported reports whether IsShiftPressed can detect anything,
// so callers can skip polling it entirely.
const ShiftPollingSupported = true

// IsShiftPressed polls the Win32 key state for the Shift key.
func IsShiftPressed() bool {
	ret, _, _ := getAsyncKeyState.Call(uintptr(vkShift))
	return ret&0x8000 != 0
}
