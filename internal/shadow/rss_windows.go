//go:build windows

package shadow

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

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

func residentBytes() uint64 {
	counters := processMemoryCounters{cb: uint32(unsafe.Sizeof(processMemoryCounters{}))}
	ok, _, _ := getProcessMemoryInfo.Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&counters)), uintptr(counters.cb))
	if ok == 0 {
		return 0
	}
	return uint64(counters.workingSet)
}
