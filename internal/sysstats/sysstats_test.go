package sysstats

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Ogstra/uade-bot/internal/shadow"
)

// TestCollectReflectsShadowPrimitivesForOwnProcess compares Collect's
// output for the process's own pid against shadow.ProcessRSSBytes and
// shadow.ProcessSwapBytes called directly, rather than against a
// hardcoded value, so this test is portable between Linux and Windows.
// Byte-for-byte equality is not asserted: RSS/swap are live readings of a
// running process, and the test process's own memory can shift by a few KB
// between the two independent syscalls below (GC, goroutine scheduling) --
// asserting exact equality would make this test flaky by construction.
// Availability must match exactly, and when available, the two readings
// must be within a generous relative tolerance of each other.
func TestCollectReflectsShadowPrimitivesForOwnProcess(t *testing.T) {
	pid := os.Getpid()
	wantRSS, wantRSSErr := shadow.ProcessRSSBytes(pid)
	wantSwap, wantSwapErr := shadow.ProcessSwapBytes(pid)

	got := Collect(pid, "")

	if got.RSSAvailable != (wantRSSErr == nil) {
		t.Fatalf("RSSAvailable=%v, want %v (err=%v)", got.RSSAvailable, wantRSSErr == nil, wantRSSErr)
	}
	if wantRSSErr == nil && !withinTolerance(got.RSSBytes, wantRSS, 0.5) {
		t.Fatalf("RSSBytes=%d, want approximately %d", got.RSSBytes, wantRSS)
	}
	if got.SwapAvailable != (wantSwapErr == nil) {
		t.Fatalf("SwapAvailable=%v, want %v (err=%v)", got.SwapAvailable, wantSwapErr == nil, wantSwapErr)
	}
	if wantSwapErr == nil && !withinTolerance(got.SwapBytes, wantSwap, 0.5) {
		t.Fatalf("SwapBytes=%d, want approximately %d", got.SwapBytes, wantSwap)
	}
}

// withinTolerance reports whether a and b are within the given relative
// tolerance of each other (e.g. 0.5 = within 50%), treating 0/0 as equal.
func withinTolerance(a, b uint64, tolerance float64) bool {
	if a == b {
		return true
	}
	max := a
	if b > max {
		max = b
	}
	if max == 0 {
		return true
	}
	var diff uint64
	if a > b {
		diff = a - b
	} else {
		diff = b - a
	}
	return float64(diff)/float64(max) <= tolerance
}

func TestCollectWithEmptyDBPathNeverReportsDBSizeAvailable(t *testing.T) {
	got := Collect(os.Getpid(), "")
	if got.DBSizeAvailable {
		t.Fatalf("expected DBSizeAvailable=false for empty dbPath, got %+v", got)
	}
}

func TestCollectWithRealFileReportsExactSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fake.db")
	if err := os.WriteFile(path, []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := Collect(os.Getpid(), path)
	if !got.DBSizeAvailable || got.DBSizeBytes != 5 {
		t.Fatalf("expected DBSizeAvailable=true DBSizeBytes=5, got %+v", got)
	}
}

func TestCollectWithNonexistentDBPathReportsUnavailableWithoutError(t *testing.T) {
	got := Collect(os.Getpid(), filepath.Join(t.TempDir(), "does-not-exist.db"))
	if got.DBSizeAvailable {
		t.Fatalf("expected DBSizeAvailable=false for a nonexistent path, got %+v", got)
	}
}

func TestFormatBytes(t *testing.T) {
	cases := []struct {
		in   uint64
		want string
	}{
		{0, "0 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
	}
	for _, c := range cases {
		if got := FormatBytes(c.in); got != c.want {
			t.Errorf("FormatBytes(%d)=%q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatOrUnavailable(t *testing.T) {
	if got := FormatOrUnavailable(false, 1024); got != "no disponible" {
		t.Fatalf("FormatOrUnavailable(false, 1024)=%q, want %q", got, "no disponible")
	}
	if got := FormatOrUnavailable(true, 1024); got != "1.0 KB" {
		t.Fatalf("FormatOrUnavailable(true, 1024)=%q, want %q", got, "1.0 KB")
	}
}
