//go:build !windows && !linux

package shadow

import "runtime"

func residentBytes() uint64 {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.Sys
}
