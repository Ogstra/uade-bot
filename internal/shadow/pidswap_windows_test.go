//go:build windows

package shadow

import (
	"os"
	"testing"
)

// TestProcessSwapBytesIsUnsupportedOnWindows documents that this is
// intentional, permanent behavior -- swap is not supported on Windows,
// not a placeholder pending a future implementation. See ProcessSwapBytes
// in pidrss_windows.go for why.
func TestProcessSwapBytesIsUnsupportedOnWindows(t *testing.T) {
	if _, err := ProcessSwapBytes(os.Getpid()); err == nil {
		t.Fatal("expected ProcessSwapBytes to always error on windows")
	}
}
