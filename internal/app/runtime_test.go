package app

import (
	"testing"
	"time"
)

// TestRecoverGoroutineConvertsPanicToLogInsteadOfCrashing proves recoverGoroutine
// contains a panic to the goroutine it runs in instead of crashing the process
// (CR-01 in 03.3-REVIEW.md). Start/JobCreated/AccountReady/JobsChanged all defer
// recoverGoroutine as their first statement (safeTick for Start's ticker case),
// so this isolated goroutine reproduces the same recover pattern they rely on.
func TestRecoverGoroutineConvertsPanicToLogInsteadOfCrashing(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer recoverGoroutine("test")
		panic("boom")
	}()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("goroutine did not return within 1s; panic was not recovered")
	}
}
