//go:build linux

package shadow

import (
	"fmt"
	"os"
)

func residentBytes() uint64 {
	data, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0
	}
	var total, resident uint64
	if _, err = fmt.Sscan(string(data), &total, &resident); err != nil {
		return 0
	}
	return resident * uint64(os.Getpagesize())
}
