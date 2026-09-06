//go:build windows

package admin

import (
	"syscall"
	"time"
)

// The Windows half of processCPU. See cpu_unix.go for why this is a syscall
// and not a dependency.
//
// GetCurrentProcess hands back a pseudo-handle, so there is nothing to close.
func processCPU() (time.Duration, bool) {
	handle, err := syscall.GetCurrentProcess()
	if err != nil {
		return 0, false
	}
	var creation, exit, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return 0, false
	}
	return span(kernel) + span(user), true
}

// Not Filetime.Nanoseconds(): that subtracts the offset between 1601 and the
// Unix epoch, which is right for a moment and nonsense for a span — it turns
// a few seconds of CPU into a hundred and fifty negative years. Kernel and
// user time are spans, counted in 100-nanosecond ticks.
func span(ft syscall.Filetime) time.Duration {
	ticks := int64(ft.HighDateTime)<<32 | int64(ft.LowDateTime)
	return time.Duration(ticks * 100)
}
