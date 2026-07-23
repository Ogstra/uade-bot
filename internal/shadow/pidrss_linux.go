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
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, fmt.Errorf("read /proc/%d/status: %w", pid, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "VmRSS:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, fmt.Errorf("malformed VmRSS line for pid %d: %q", pid, line)
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse VmRSS for pid %d: %w", pid, err)
		}
		return kb * 1024, nil
	}
	return 0, fmt.Errorf("VmRSS line not found in /proc/%d/status", pid)
}
