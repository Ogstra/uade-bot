//go:build windows

package shadow

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ProcessRSSBytes reads the working set size of an arbitrary process (not
// necessarily the calling one), unlike residentBytes in rss_windows.go which
// only ever calls GetProcessMemoryInfo against windows.CurrentProcess(). This
// is the primitive cmd/rsscompare uses to measure two separately-spawned full
// processes (the Go binary and node src/bot.js).
func ProcessRSSBytes(pid int) (uint64, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_VM_READ, false, uint32(pid))
	if err != nil {
		return 0, fmt.Errorf("open process %d: %w", pid, err)
	}
	defer windows.CloseHandle(handle)

	counters := processMemoryCounters{cb: uint32(unsafe.Sizeof(processMemoryCounters{}))}
	ok, _, callErr := getProcessMemoryInfo.Call(uintptr(handle), uintptr(unsafe.Pointer(&counters)), uintptr(counters.cb))
	if ok == 0 {
		return 0, fmt.Errorf("GetProcessMemoryInfo for pid %d: %w", pid, callErr)
	}
	return uint64(counters.workingSet), nil
}
