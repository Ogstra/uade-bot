//go:build windows

package sysstats

import "golang.org/x/sys/windows"

// processMemoryCounters mirrors PROCESS_MEMORY_COUNTERS from psapi.h, the
// struct GetProcessMemoryInfo fills in for ProcessRSSBytes.
type processMemoryCounters struct {
	cb                         uint32
	pageFaultCount             uint32
	peakWorkingSet, workingSet uintptr
	quotaPeakPagedPool         uintptr
	quotaPagedPool             uintptr
	quotaPeakNonPagedPool      uintptr
	quotaNonPagedPool          uintptr
	pagefileUsage              uintptr
	peakPagefileUsage          uintptr
}

var getProcessMemoryInfo = windows.NewLazySystemDLL("psapi.dll").NewProc("GetProcessMemoryInfo")
