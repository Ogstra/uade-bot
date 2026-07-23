package shadow

import (
	"os"
	"testing"
)

func TestProcessRSSBytesReturnsNonZeroForOwnProcess(t *testing.T) {
	rss, err := ProcessRSSBytes(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if rss == 0 {
		t.Fatal("expected a nonzero RSS reading for the calling process's own pid")
	}
}
