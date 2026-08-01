//go:build linux

package shadow

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// ProcessRSSBytes reads the resident set size of an arbitrary process (not
// necessarily the calling one) via /proc/<pid>/status, unlike residentBytes
// in rss_linux.go which only ever reads /proc/self/statm for this process's
// own memory. This is the primitive cmd/rsscompare uses to measure two
// separately-spawned full processes (the Go binary and node src/bot.js).
func ProcessRSSBytes(pid int) (uint64, error) {
	return readProcStatusKB(pid, "VmRSS:")
}

// ProcessSwapBytes reads the swap usage of an arbitrary process via the same
// /proc/<pid>/status file ProcessRSSBytes already reads, looking for the
// VmSwap: line instead of VmRSS:. internal/sysstats.Collect is its only
// caller in production, feeding both the dashboard's "Resumen de salud" and
// Discord's /admin-stats.
func ProcessSwapBytes(pid int) (uint64, error) {
	return readProcStatusKB(pid, "VmSwap:")
}

// readProcStatusKB opens /proc/<pid>/status, finds the line starting with
// prefix, parses its second whitespace-separated field as kB, and returns
// the value in bytes. Shared by ProcessRSSBytes (prefix "VmRSS:") and
// ProcessSwapBytes (prefix "VmSwap:") since both fields live in the same
// file with the same "<Prefix> <N> kB" format.
func readProcStatusKB(pid int, prefix string) (uint64, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, fmt.Errorf("read /proc/%d/status: %w", pid, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, fmt.Errorf("malformed %s line for pid %d: %q", prefix, pid, line)
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse %s for pid %d: %w", prefix, pid, err)
		}
		return kb * 1024, nil
	}
	return 0, fmt.Errorf("%s line not found in /proc/%d/status", prefix, pid)
}
