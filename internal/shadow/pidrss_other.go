//go:build !windows && !linux

package shadow

import "fmt"

// ProcessRSSBytes has no portable implementation for reading an arbitrary
// process's resident set size outside Linux (/proc) and Windows
// (GetProcessMemoryInfo) without a third-party dependency. cmd/rsscompare is
// a manually-invoked operator diagnostic (see its package doc) that must at
// least compile on every platform this module builds for; on unsupported
// platforms it fails loudly here rather than silently returning a
// misleading zero.
func ProcessRSSBytes(pid int) (uint64, error) {
	return 0, fmt.Errorf("ProcessRSSBytes: unsupported on this platform (pid %d) — cmd/rsscompare requires linux or windows", pid)
}

// ProcessSwapBytes has no portable implementation outside Linux (/proc) on
// this platform. Same pattern as ProcessRSSBytes above: fail loudly instead
// of returning a misleading zero.
func ProcessSwapBytes(pid int) (uint64, error) {
	return 0, fmt.Errorf("ProcessSwapBytes: unsupported on this platform (pid %d) — requires linux", pid)
}
