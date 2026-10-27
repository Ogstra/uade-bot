package entra

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	userA = "111111111111111111"
	userB = "222222222222222222"
)

type finish struct {
	user   string
	status LoginStatus
}

type managerRig struct {
	fake     *scripted
	clock    *clock
	manager  *Manager
	mu       sync.Mutex
	dead     []string
	finished chan finish
}

func newManagerRig(t *testing.T) *managerRig {
	t.Helper()
	rig := &managerRig{fake: newScripted(), clock: newClock(), finished: make(chan finish, 8)}
	rig.manager = &Manager{
		Config: testConfig(),
		HTTP:   rig.fake,
		Dir:    t.TempDir(),
		Secret: testSecret,
		Now:    rig.clock.Now,
		Sleep:  (&sleeps{clock: rig.clock}).Sleep,
		OnDead: func(user string) {
			rig.mu.Lock()
			rig.dead = append(rig.dead, user)
			rig.mu.Unlock()
		},
		OnLoginFinish: func(user string, status LoginStatus) { rig.finished <- finish{user, status} },
	}
	return rig
}

func (r *managerRig) waitFinish(t *testing.T) finish {
	t.Helper()
	select {
	case f := <-r.finished:
		return f
	case <-time.After(5 * time.Second):
		t.Fatal("sign-in never finished")
		return finish{}
	}
}

// Every user can sign in, each with their own account and their own file.
func TestEveryUserGetsTheirOwnSession(t *testing.T) {
	rig := newManagerRig(t)
	ctx := context.Background()
	for _, tc := range []struct{ user, at, rt string }{{userA, "AT-A", "RT-A"}, {userB, "AT-B", "RT-B"}} {
		s, err := rig.manager.Session(tc.user)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Adopt(ctx, Tokens{AccessToken: tc.at, RefreshToken: tc.rt, ExpiresAt: rig.clock.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}

	for _, tc := range []struct{ user, want string }{{userA, "AT-A"}, {userB, "AT-B"}} {
		s, _ := rig.manager.Session(tc.user)
		if token, _ := s.Token(ctx); token != tc.want {
			t.Fatalf("%s token = %q, want %q", tc.user, token, tc.want)
		}
	}
	for _, user := range []string{userA, userB} {
		if _, err := os.Stat(filepath.Join(rig.manager.Dir, user+".json")); err != nil {
			t.Fatalf("no file for %s: %v", user, err)
		}
	}
}

// Copying user A's file over user B's must not hand A's session to B.
func TestOneUsersFileDoesNotOpenAsAnother(t *testing.T) {
	rig := newManagerRig(t)
	a, _ := rig.manager.Session(userA)
	_ = a.Adopt(context.Background(), Tokens{AccessToken: "AT-A", RefreshToken: "RT-A", ExpiresAt: rig.clock.Now().Add(time.Hour)})

	raw, _ := os.ReadFile(filepath.Join(rig.manager.Dir, userA+".json"))
	_ = os.WriteFile(filepath.Join(rig.manager.Dir, userB+".json"), raw, 0o600)

	got, err := rig.manager.store(userB).Load()
	if !errors.Is(err, ErrStoreUnreadable) || got != "" {
		t.Fatalf("A's file opened as B: %q, %v", got, err)
	}
}

// The user id becomes a filename: anything that is not a snowflake is refused.
func TestUserIDsThatAreNotSnowflakesAreRefused(t *testing.T) {
	rig := newManagerRig(t)
	for _, bad := range []string{"", "../../etc/passwd", "123", "1111111111111111111/../x", "abcdefghijklmnopqr"} {
		if _, err := rig.manager.Session(bad); !errors.Is(err, ErrInvalidUser) {
			t.Fatalf("Session(%q) err = %v, want ErrInvalidUser", bad, err)
		}
		if _, err := rig.manager.BeginLogin(context.Background(), bad); !errors.Is(err, ErrInvalidUser) {
			t.Fatalf("BeginLogin(%q) err = %v, want ErrInvalidUser", bad, err)
		}
	}
}

// End to end: Begin hands out the code, polling happens on the server, and
// the result is reported to the user who asked -- without any token in it.
func TestDeviceCodeSignInEndToEnd(t *testing.T) {
	rig := newManagerRig(t)
	rig.fake.on("/devicecode", reply{status: 200, body: deviceCodeJSON})
	rig.fake.on("/token",
		reply{status: 400, body: errJSON("authorization_pending")},
		reply{status: 200, body: tokenJSON("AT-SECRET", "RT-SECRET", 3600)},
	)

	status, err := rig.manager.BeginLogin(context.Background(), userA)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != LoginPending || status.UserCode != "ABCD-EFGH" || status.VerificationURI == "" {
		t.Fatalf("begin status = %+v", status)
	}

	f := rig.waitFinish(t)

	if f.user != userA || f.status.State != LoginOK {
		t.Fatalf("finish = %+v", f)
	}
	s, _ := rig.manager.Session(userA)
	if token, _ := s.Token(context.Background()); token != "AT-SECRET" {
		t.Fatalf("session token = %q", token)
	}
	for _, st := range []LoginStatus{status, f.status, rig.manager.LoginStatus(userA)} {
		if text := fmtLogin(st); containsAny(text, "AT-SECRET", "RT-SECRET", "DEV-SECRET") {
			t.Fatalf("login status leaks a secret: %s", text)
		}
	}
}

// Pressing the button twice while a code is pending returns the same code
// instead of opening a second grant that would race the first.
func TestBeginReusesThePendingCode(t *testing.T) {
	rig := newManagerRig(t)
	rig.fake.on("/devicecode", reply{status: 200, body: deviceCodeJSON})
	// Never completes: the grant stays pending for the whole test.
	block := make(chan struct{})
	defer close(block)
	rig.manager.Sleep = func(ctx context.Context, d time.Duration) error {
		select {
		case <-block:
			return context.Canceled
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	first, _ := rig.manager.BeginLogin(context.Background(), userA)
	second, _ := rig.manager.BeginLogin(context.Background(), userA)

	if first.UserCode != second.UserCode {
		t.Fatalf("second Begin opened a new code: %q vs %q", first.UserCode, second.UserCode)
	}
	if n := rig.fake.count("/devicecode"); n != 1 {
		t.Fatalf("devicecode calls = %d, want 1", n)
	}
}

// One user's pending sign-in neither blocks nor replaces another's.
func TestPendingSignInsAreIndependentPerUser(t *testing.T) {
	rig := newManagerRig(t)
	rig.fake.on("/devicecode",
		reply{status: 200, body: deviceCodeJSON},
		reply{status: 200, body: strings.Replace(deviceCodeJSON, "ABCD-EFGH", "WXYZ-1234", 1)},
	)
	block := make(chan struct{})
	defer close(block)
	rig.manager.Sleep = func(ctx context.Context, d time.Duration) error {
		select {
		case <-block:
			return context.Canceled
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	a, _ := rig.manager.BeginLogin(context.Background(), userA)
	b, _ := rig.manager.BeginLogin(context.Background(), userB)

	if a.UserCode == b.UserCode {
		t.Fatal("two users were handed the same code")
	}
	if rig.manager.LoginStatus(userA).UserCode != a.UserCode {
		t.Fatal("user B's sign-in replaced user A's")
	}
}

func TestFailedSignInIsSummarizedWithoutMicrosoftDetail(t *testing.T) {
	rig := newManagerRig(t)
	rig.fake.on("/devicecode", reply{status: 200, body: deviceCodeJSON})
	rig.fake.on("/token", reply{status: 400, body: errJSON("access_denied")})

	if _, err := rig.manager.BeginLogin(context.Background(), userA); err != nil {
		t.Fatal(err)
	}
	f := rig.waitFinish(t)

	if f.status.State != LoginFailed || f.status.Message == "" {
		t.Fatalf("finish = %+v", f)
	}
	if containsAny(f.status.Message, "AADSTS", "secret-trace-id", "@uade.edu.ar", "access_denied") {
		t.Fatalf("message leaks Microsoft detail: %q", f.status.Message)
	}
}

// A session dying notifies its own user, and only them.
func TestDeadSessionNotifiesOnlyItsOwner(t *testing.T) {
	rig := newManagerRig(t)
	ctx := context.Background()
	for _, user := range []string{userA, userB} {
		s, _ := rig.manager.Session(user)
		_ = s.Adopt(ctx, Tokens{AccessToken: "AT-" + user, RefreshToken: "RT-" + user, ExpiresAt: rig.clock.Now().Add(time.Hour)})
	}
	rig.fake.on("/token", reply{status: 400, body: errJSON("invalid_grant")})
	rig.clock.advance(50 * time.Minute)

	a, _ := rig.manager.Session(userA)
	_, _ = a.Token(ctx)

	rig.mu.Lock()
	defer rig.mu.Unlock()
	if len(rig.dead) != 1 || rig.dead[0] != userA {
		t.Fatalf("dead notifications = %v, want only %s", rig.dead, userA)
	}
}

// Stored sessions come back after a restart, so idle users keep renewing.
func TestLoadAllRestoresSessionsAfterRestart(t *testing.T) {
	rig := newManagerRig(t)
	a, _ := rig.manager.Session(userA)
	_ = a.Adopt(context.Background(), Tokens{AccessToken: "AT", RefreshToken: "RT-A", ExpiresAt: rig.clock.Now().Add(time.Hour)})
	_ = os.WriteFile(filepath.Join(rig.manager.Dir, "not-a-user.json"), []byte("x"), 0o600)

	restarted := &Manager{Config: testConfig(), HTTP: rig.fake, Dir: rig.manager.Dir, Secret: testSecret, Now: rig.clock.Now}
	if err := restarted.LoadAll(); err != nil {
		t.Fatal(err)
	}

	if got := len(restarted.active()); got != 1 {
		t.Fatalf("active sessions after restart = %d, want 1", got)
	}
}

func TestRenewAllRefreshesOnlyActiveSessionsNearExpiry(t *testing.T) {
	rig := newManagerRig(t)
	ctx := context.Background()
	a, _ := rig.manager.Session(userA)
	_ = a.Adopt(ctx, Tokens{AccessToken: "AT-A", RefreshToken: "RT-A", ExpiresAt: rig.clock.Now().Add(time.Hour)})
	_, _ = rig.manager.Session(userB) // signed out: nothing to renew
	rig.fake.on("/token", reply{status: 200, body: tokenJSON("AT-A2", "RT-A2", 3600)})

	rig.manager.RenewAll(ctx)
	if n := rig.fake.count("/token"); n != 0 {
		t.Fatalf("renewed %d times with an hour left, want 0", n)
	}

	rig.clock.advance(40 * time.Minute)
	rig.manager.RenewAll(ctx)
	if n := rig.fake.count("/token"); n != 1 {
		t.Fatalf("token calls = %d, want 1 (only the active session)", n)
	}
}

func TestForgetSignsOutAndDeletesTheFile(t *testing.T) {
	rig := newManagerRig(t)
	a, _ := rig.manager.Session(userA)
	_ = a.Adopt(context.Background(), Tokens{AccessToken: "AT", RefreshToken: "RT", ExpiresAt: rig.clock.Now().Add(time.Hour)})

	if err := rig.manager.Forget(userA); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(rig.manager.Dir, userA+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file still present: %v", err)
	}
	s, _ := rig.manager.Session(userA)
	if s.Status().State != StateNone {
		t.Fatalf("state after Forget = %q, want none", s.Status().State)
	}
}

func fmtLogin(st LoginStatus) string {
	return st.Message + "|" + st.UserCode + "|" + st.VerificationURI + "|" + string(st.State)
}
