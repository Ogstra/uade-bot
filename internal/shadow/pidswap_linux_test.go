//go:build linux

package shadow

import (
	"os"
	"testing"
)

func TestProcessSwapBytesReadsOwnProcessWithoutError(t *testing.T) {
	// Intentionally does NOT assert a nonzero value -- unlike RSS, VmSwap
	// being 0 kB for the test process is a legitimate reading, not a
	// failure to be treated as such.
	if _, err := ProcessSwapBytes(os.Getpid()); err != nil {
		t.Fatal(err)
	}
}

func TestProcessSwapBytesReturnsErrorForNonexistentPID(t *testing.T) {
	if _, err := ProcessSwapBytes(-1); err == nil {
		t.Fatal("expected an error for a nonexistent pid")
	}
}
