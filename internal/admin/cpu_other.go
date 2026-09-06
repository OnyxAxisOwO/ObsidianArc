//go:build !unix && !windows

package admin

import "time"

// Anywhere neither cpu_unix.go nor cpu_windows.go builds. The page is told
// there is no answer rather than shown a zero, which would read as an idle
// instance instead of an unanswerable question.
func processCPU() (time.Duration, bool) { return 0, false }
