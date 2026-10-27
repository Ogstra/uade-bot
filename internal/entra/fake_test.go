package entra

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// scripted is a Doer that answers from a queue per endpoint and records every
// request it saw. Nothing here opens a socket.
type scripted struct {
	mu        sync.Mutex
	responses map[string][]reply
	// lastReply repeats once a queue runs dry, so a test can script "always
	// fail from here on" with a single reply. Kept apart from the queue so
	// replies appended later are still served in order.
	lastReply map[string]reply
	requests  []recorded
	// gate, when set, blocks every token-endpoint call until closed. Lets a
	// test pile up concurrent callers on one in-flight refresh.
	gate chan struct{}
}

type reply struct {
	status int
	body   string
	err    error
}

type recorded struct {
	path   string
	form   url.Values
	bearer string
}

func newScripted() *scripted {
	return &scripted{responses: map[string][]reply{}, lastReply: map[string]reply{}}
}

func (s *scripted) on(suffix string, replies ...reply) *scripted {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.responses[suffix] = append(s.responses[suffix], replies...)
	return s
}

func (s *scripted) Do(req *http.Request) (*http.Response, error) {
	var form url.Values
	if req.Body != nil {
		raw, _ := io.ReadAll(req.Body)
		form, _ = url.ParseQuery(string(raw))
	}
	bearer := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")

	s.mu.Lock()
	s.requests = append(s.requests, recorded{path: req.URL.Path, form: form, bearer: bearer})
	gate := s.gate
	s.mu.Unlock()
	if gate != nil && strings.HasSuffix(req.URL.Path, "/token") {
		<-gate
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for suffix, queue := range s.responses {
		if !strings.HasSuffix(req.URL.Path, suffix) {
			continue
		}
		var next reply
		if len(queue) > 0 {
			next = queue[0]
			s.responses[suffix] = queue[1:]
			s.lastReply[suffix] = next
		} else if sticky, ok := s.lastReply[suffix]; ok {
			next = sticky
		} else {
			continue
		}
		if next.err != nil {
			return nil, next.err
		}
		return &http.Response{StatusCode: next.status, Body: io.NopCloser(bytes.NewBufferString(next.body)), Header: http.Header{}}, nil
	}
	return nil, errors.New("scripted: no reply for " + req.URL.Path)
}

func (s *scripted) count(suffix string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, r := range s.requests {
		if strings.HasSuffix(r.path, suffix) {
			n++
		}
	}
	return n
}

func (s *scripted) last(suffix string) recorded {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.requests) - 1; i >= 0; i-- {
		if strings.HasSuffix(s.requests[i].path, suffix) {
			return s.requests[i]
		}
	}
	return recorded{}
}

// clock is a manually advanced time source shared by client and session.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock() *clock { return &clock{now: time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)} }

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// sleeps records each requested wait and advances the clock by it, so polling
// runs instantly while still exercising the interval logic.
type sleeps struct {
	clock *clock
	mu    sync.Mutex
	got   []time.Duration
}

func (s *sleeps) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	s.got = append(s.got, d)
	s.mu.Unlock()
	s.clock.advance(d)
	return nil
}

func testConfig() Config {
	return Config{TenantID: "tenant-1", Resource: "https://contoso.sharepoint.com", Authority: "https://login.test"}
}

func tokenJSON(access, refresh string, expiresIn int) string {
	body := `{"access_token":"` + access + `","expires_in":` + strconv.Itoa(expiresIn)
	if refresh != "" {
		body += `,"refresh_token":"` + refresh + `"`
	}
	return body + `}`
}

func errJSON(code string) string {
	// Includes a description on purpose: tests assert it never leaks.
	return `{"error":"` + code + `","error_description":"AADSTS70000: secret-trace-id-9f3c for user@uade.edu.ar"}`
}

// testSecret is short on purpose and only for tests; the store derives its
// key with scrypt either way.
var testSecret = []byte("test-secret-not-for-production")

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return &Store{Path: t.TempDir() + "/entra-token.json", Secret: testSecret}
}
