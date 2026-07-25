package app

import (
	"bytes"
	"log"
	"strings"
	"testing"
	"time"
)

// TestRecoverGoroutineConvertsPanicToLogInsteadOfCrashing proves recoverGoroutine
// contains a panic to the goroutine it runs in instead of crashing the process
// (CR-01 in 03.3-REVIEW.md), AND that it never leaks the arbitrary panic value
// into the log (T-03.3-13-01 / GO-02 / D-06). Start/JobCreated/AccountReady/
// JobsChanged all defer recoverGoroutine as their first statement (safeTick for
// Start's ticker case), so this isolated goroutine reproduces the same recover
// pattern they rely on, with a sentinel that simulates a leaked UADE password,
// Discord token, and start-URL query param.
//
// Not run with t.Parallel: the standard logger is global process state, and
// this test captures/restores it.
func TestRecoverGoroutineConvertsPanicToLogInsteadOfCrashing(t *testing.T) {
	origWriter := log.Writer()
	origFlags := log.Flags()
	origPrefix := log.Prefix()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	log.SetFlags(0)
	log.SetPrefix("")
	defer func() {
		log.SetOutput(origWriter)
		log.SetFlags(origFlags)
		log.SetPrefix(origPrefix)
	}()

	const (
		sensitivePassword = "hunter2-uade-password"
		sensitiveToken    = "discord-token-abc123"
		sensitiveURLParam = "param=eyJhbGciOiJI"
	)
	sentinel := sensitivePassword + " " + sensitiveToken + " https://inscripcionespia.uade.edu.ar/x?" + sensitiveURLParam

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer recoverGoroutine("test")
		panic(sentinel)
	}()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("goroutine did not return within 1s; panic was not recovered")
	}

	logged := buf.String()

	prohibited := []string{sentinel, sensitivePassword, sensitiveToken, sensitiveURLParam}
	for _, fragment := range prohibited {
		if strings.Contains(logged, fragment) {
			t.Fatal("runtime recovery log contains prohibited sensitive content")
		}
	}

	const allowedMarker = "runtime goroutine panic recovered stage=test"
	if !strings.Contains(logged, allowedMarker) {
		t.Fatalf("recovery log missing expected marker %q", allowedMarker)
	}
}
