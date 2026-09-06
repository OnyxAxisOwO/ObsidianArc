//go:build unix

package admin

import (
	"syscall"
	"time"
)

// The CPU time this process has used, user plus system.
//
// Getrusage rather than a process-metrics library: the one question the page
// asks is how busy this instance is, that is a single syscall per platform
// family, and the alternative is several thousand lines of dependency to ask
// it. The Windows half is in cpu_windows.go.
func processCPU() (time.Duration, bool) {
	var used syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &used); err != nil {
		return 0, false
	}
	return time.Duration(used.Utime.Nano() + used.Stime.Nano()), true
}
