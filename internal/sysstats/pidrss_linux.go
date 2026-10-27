//go:build linux

package sysstats

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// ProcessRSSBytes reads the resident set size of an arbitrary process via
// /proc/<pid>/status. Collect calls it for the bot's own pid, feeding the
// dashboard's "Resumen de salud" and Discord's /admin-stats.
func ProcessRSSBytes(pid int) (uint64, error) {
	return readProcStatusKB(pid, "VmRSS:")
}

// ProcessSwapBytes reads the swap usage of an arbitrary process via the same
// /proc/<pid>/status file ProcessRSSBytes already reads, looking for the
// VmSwap: line instead of VmRSS:. Same consumer as ProcessRSSBytes.
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
