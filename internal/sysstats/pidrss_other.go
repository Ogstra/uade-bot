//go:build !windows && !linux

package sysstats

import "fmt"

// ProcessRSSBytes has no portable implementation for reading an arbitrary
// process's resident set size outside Linux (/proc) and Windows
// (GetProcessMemoryInfo) without a third-party dependency. It fails loudly
// rather than returning a misleading zero, and Collect reports the metric as
// unavailable.
func ProcessRSSBytes(pid int) (uint64, error) {
	return 0, fmt.Errorf("ProcessRSSBytes: unsupported on this platform (pid %d)", pid)
}

// ProcessSwapBytes has no portable implementation outside Linux (/proc) on
// this platform. Same pattern as ProcessRSSBytes above: fail loudly instead
// of returning a misleading zero.
func ProcessSwapBytes(pid int) (uint64, error) {
	return 0, fmt.Errorf("ProcessSwapBytes: unsupported on this platform (pid %d) — requires linux", pid)
}
