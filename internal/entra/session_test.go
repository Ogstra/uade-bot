package entra

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type sessionRig struct {
	fake    *scripted
	clock   *clock
	store   *Store
	session *Session
	dead    atomic.Int32
}

func newSessionRig(t *testing.T, refresh string) *sessionRig {
	t.Helper()
	rig := &sessionRig{fake: newScripted(), clock: newClock(), store: newTestStore(t)}
	if refresh != "" {
		if err := rig.store.Save(refresh); err != nil {
			t.Fatal(err)
		}
	}
	rig.session = &Session{
		Client: &Client{Config: testConfig(), HTTP: rig.fake, Now: rig.clock.Now},
		Store:  rig.store,
		Now:    rig.clock.Now,
		OnDead: func() { rig.dead.Add(1) },
	}
	if err := rig.session.Load(); err != nil {
		t.Fatal(err)
	}
	return rig
}

func TestTokenRefreshesOnFirstUseAfterRestart(t *testing.T) {
	rig := newSessionRig(t, "RT-0")
	rig.fake.on("/token", reply{status: 200, body: tokenJSON("AT-1", "RT-1", 3600)})

	token, err := rig.session.Token(context.Background())
	if err != nil || token != "AT-1" {
		t.Fatalf("Token = %q, %v", token, err)
	}
	if got := rig.fake.last("/token").form.Get("refresh_token"); got != "RT-0" {
		t.Fatalf("refreshed with %q, want the stored RT-0", got)
	}
}

// Microsoft rotates refresh tokens: the new one must replace the old one on
// disk, or the next restart presents a token Microsoft already burned.
func TestRefreshTokenRotationIsPersisted(t *testing.T) {
	rig := newSessionRig(t, "RT-0")
	rig.fake.on("/token", reply{status: 200, body: tokenJSON("AT-1", "RT-1", 3600)})

	if _, err := rig.session.Token(context.Background()); err != nil {
		t.Fatal(err)
	}

	stored, err := rig.store.Load()
	if err != nil || stored != "RT-1" {
		t.Fatalf("stored refresh token = %q, %v; want the rotated RT-1", stored, err)
	}
}

func TestRefreshKeepsThePreviousTokenWhenNoneIsReturned(t *testing.T) {
	rig := newSessionRig(t, "RT-0")
	rig.fake.on("/token", reply{status: 200, body: tokenJSON("AT-1", "", 3600)})

	if _, err := rig.session.Token(context.Background()); err != nil {
		t.Fatal(err)
	}

	if stored, _ := rig.store.Load(); stored != "RT-0" {
		t.Fatalf("stored = %q, want RT-0 kept when Microsoft sends no new one", stored)
	}
}

func TestTokenIsReusedUntilTheRenewalWindow(t *testing.T) {
	rig := newSessionRig(t, "RT-0")
	rig.fake.on("/token",
		reply{status: 200, body: tokenJSON("AT-1", "RT-1", 3600)},
		reply{status: 200, body: tokenJSON("AT-2", "RT-2", 3600)},
	)
	ctx := context.Background()
	_, _ = rig.session.Token(ctx)

	rig.clock.advance(30 * time.Minute) // 30 min left: outside the 25 min window
	if token, _ := rig.session.Token(ctx); token != "AT-1" {
		t.Fatalf("token = %q, want AT-1 reused", token)
	}
	if rig.fake.count("/token") != 1 {
		t.Fatalf("token calls = %d, want 1", rig.fake.count("/token"))
	}

	rig.clock.advance(10 * time.Minute) // 20 min left: inside the window
	if token, _ := rig.session.Token(ctx); token != "AT-2" {
		t.Fatalf("token = %q, want AT-2 after early renewal", token)
	}
}

// Many concurrent callers on an expired token share exactly one refresh. Two
// parallel refreshes with one rotating token would race each other.
func TestConcurrentCallersShareASingleRefresh(t *testing.T) {
	rig := newSessionRig(t, "RT-0")
	rig.fake.on("/token", reply{status: 200, body: tokenJSON("AT-1", "RT-1", 3600)})
	rig.fake.gate = make(chan struct{})

	const callers = 20
	var wg sync.WaitGroup
	tokens := make([]string, callers)
	errs := make([]error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tokens[i], errs[i] = rig.session.Token(context.Background())
		}(i)
	}
	// Let every caller reach the in-flight refresh before it completes.
	deadline := time.Now().Add(2 * time.Second)
	for rig.fake.count("/token") == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	close(rig.fake.gate)
	wg.Wait()

	if n := rig.fake.count("/token"); n != 1 {
		t.Fatalf("token calls = %d, want exactly 1", n)
	}
	for i := range tokens {
		if errs[i] != nil || tokens[i] != "AT-1" {
			t.Fatalf("caller %d got %q, %v", i, tokens[i], errs[i])
		}
	}
}

// invalid_grant kills the session: secrets dropped, file cleared, OnDead fired
// once -- and only once, however many requests hit the dead session after.
func TestPermanentFailureKillsTheSessionAndNotifiesOnce(t *testing.T) {
	rig := newSessionRig(t, "RT-0")
	rig.fake.on("/token", reply{status: 200, body: tokenJSON("AT-1", "RT-1", 3600)})
	ctx := context.Background()
	_, _ = rig.session.Token(ctx)

	rig.fake.on("/token", reply{status: 400, body: errJSON("invalid_grant")})
	rig.clock.advance(50 * time.Minute)

	for i := 0; i < 5; i++ {
		if _, err := rig.session.Token(ctx); err == nil {
			t.Fatal("dead session still produced a token")
		}
	}

	if st := rig.session.Status(); st.State != StateDead {
		t.Fatalf("state = %q, want dead", st.State)
	}
	if n := rig.dead.Load(); n != 1 {
		t.Fatalf("OnDead fired %d times, want 1", n)
	}
	if _, err := rig.store.Load(); !errors.Is(err, ErrNoToken) {
		t.Fatalf("dead refresh token still on disk: %v", err)
	}
}

// Network failure or 5xx: the session is fine. The still-valid access token
// keeps being served and nothing is cleared or notified.
func TestTransientFailureKeepsTheCurrentToken(t *testing.T) {
	rig := newSessionRig(t, "RT-0")
	rig.fake.on("/token", reply{status: 200, body: tokenJSON("AT-1", "RT-1", 3600)})
	ctx := context.Background()
	_, _ = rig.session.Token(ctx)

	rig.fake.on("/token", reply{status: 503, body: "busy"}, reply{err: errors.New("dial tcp: timeout")})
	rig.clock.advance(40 * time.Minute) // inside the renewal window, not yet expired

	for i := 0; i < 2; i++ {
		token, err := rig.session.Token(ctx)
		if err != nil || token != "AT-1" {
			t.Fatalf("attempt %d: Token = %q, %v; want AT-1 kept", i, token, err)
		}
	}
	if st := rig.session.Status(); st.State != StateActive {
		t.Fatalf("state = %q, want active", st.State)
	}
	if rig.dead.Load() != 0 {
		t.Fatal("transient failure notified a dead session")
	}
	if stored, _ := rig.store.Load(); stored != "RT-1" {
		t.Fatalf("stored = %q, want RT-1 untouched", stored)
	}
}

// A new token is only adopted after the resource accepts it. A failed
// validation leaves the previous session serving requests.
func TestRefreshedTokenIsNotAdoptedWhenTheResourceRejectsIt(t *testing.T) {
	rig := newSessionRig(t, "RT-0")
	rig.fake.on("/token",
		reply{status: 200, body: tokenJSON("AT-1", "RT-1", 3600)},
		reply{status: 200, body: tokenJSON("AT-BAD", "RT-2", 3600)},
	)
	rig.session.Validate = func(_ context.Context, token string) error {
		if token == "AT-BAD" {
			return errors.New("rejected")
		}
		return nil
	}
	ctx := context.Background()
	_, _ = rig.session.Token(ctx)
	rig.clock.advance(40 * time.Minute)

	token, err := rig.session.Token(ctx)

	if err != nil || token != "AT-1" {
		t.Fatalf("Token = %q, %v; want the previous AT-1", token, err)
	}
	// The rotated refresh token is still kept: Microsoft already burned RT-1.
	if stored, _ := rig.store.Load(); stored != "RT-2" {
		t.Fatalf("stored = %q, want RT-2", stored)
	}
}

func TestAdoptValidatesBeforeReplacingTheSession(t *testing.T) {
	rig := newSessionRig(t, "RT-0")
	rig.fake.on("/token", reply{status: 200, body: tokenJSON("AT-1", "RT-1", 3600)})
	ctx := context.Background()
	_, _ = rig.session.Token(ctx)
	rig.session.Validate = func(context.Context, string) error { return errors.New("rejected") }

	err := rig.session.Adopt(ctx, Tokens{AccessToken: "AT-NEW", RefreshToken: "RT-NEW", ExpiresAt: rig.clock.Now().Add(time.Hour)})

	if !IsPermanent(err) {
		t.Fatalf("err = %v, want a permanent failure of this sign-in", err)
	}
	if token, _ := rig.session.Token(ctx); token != "AT-1" {
		t.Fatalf("token = %q, want the old session intact", token)
	}
	if stored, _ := rig.store.Load(); stored != "RT-1" {
		t.Fatalf("stored = %q, want RT-1 intact", stored)
	}
}

func TestAdoptRejectsTokensWithoutARefreshToken(t *testing.T) {
	rig := newSessionRig(t, "")
	err := rig.session.Adopt(context.Background(), Tokens{AccessToken: "AT", ExpiresAt: rig.clock.Now().Add(time.Hour)})
	if !IsPermanent(err) {
		t.Fatalf("err = %v: a session that cannot renew must be refused up front", err)
	}
}

// After dying, a fresh sign-in revives the session and re-arms the one-shot
// notification for the next death.
func TestAdoptRevivesADeadSession(t *testing.T) {
	rig := newSessionRig(t, "RT-0")
	rig.fake.on("/token", reply{status: 400, body: errJSON("invalid_grant")})
	ctx := context.Background()
	_, _ = rig.session.Token(ctx)

	if err := rig.session.Adopt(ctx, Tokens{AccessToken: "AT-9", RefreshToken: "RT-9", ExpiresAt: rig.clock.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if st := rig.session.Status(); st.State != StateActive {
		t.Fatalf("state = %q, want active", st.State)
	}

	rig.fake.on("/token", reply{status: 400, body: errJSON("invalid_grant")})
	rig.clock.advance(50 * time.Minute)
	_, _ = rig.session.Token(ctx)
	if n := rig.dead.Load(); n != 2 {
		t.Fatalf("OnDead fired %d times, want 2 (one per death)", n)
	}
}

func TestUnreadableStoreStartsWithoutASession(t *testing.T) {
	store := newTestStore(t)
	_ = store.Save("RT")
	session := &Session{Client: &Client{Config: testConfig(), HTTP: newScripted()}, Store: &Store{Path: store.Path, Secret: []byte("wrong")}}

	err := session.Load()

	if !errors.Is(err, ErrStoreUnreadable) {
		t.Fatalf("err = %v, want ErrStoreUnreadable", err)
	}
	if st := session.Status(); st.State != StateNone {
		t.Fatalf("state = %q, want none", st.State)
	}
	if _, err := session.Token(context.Background()); err == nil {
		t.Fatal("unreadable store produced a token")
	}
}

// The request getter: every request reads the current token, and the Bearer
// header carries it.
func TestDoSendsTheCurrentBearerToken(t *testing.T) {
	rig := newSessionRig(t, "RT-0")
	rig.fake.on("/token", reply{status: 200, body: tokenJSON("AT-1", "RT-1", 3600)})
	rig.fake.on("/_api/web", reply{status: 200, body: "{}"})

	req, _ := http.NewRequest(http.MethodGet, "https://contoso.sharepoint.com/_api/web", nil)
	resp, err := rig.session.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if got := rig.fake.last("/_api/web").bearer; got != "AT-1" {
		t.Fatalf("bearer = %q, want AT-1", got)
	}
	if req.Header.Get("Authorization") != "" {
		t.Fatal("Do mutated the caller's request")
	}
}

// 401 under Bearer: one forced renewal and one retry. A second 401 means the
// resource has stopped accepting this session.
func TestDoRetriesOnceThenKillsTheSessionOnRepeated401(t *testing.T) {
	rig := newSessionRig(t, "RT-0")
	rig.fake.on("/token",
		reply{status: 200, body: tokenJSON("AT-1", "RT-1", 3600)},
		reply{status: 200, body: tokenJSON("AT-2", "RT-2", 3600)},
	)
	rig.fake.on("/_api/web", reply{status: 401}, reply{status: 401})

	req, _ := http.NewRequest(http.MethodGet, "https://contoso.sharepoint.com/_api/web", nil)
	resp, err := rig.session.Do(req)
	if err == nil {
		resp.Body.Close()
	}

	if rig.fake.count("/_api/web") != 2 {
		t.Fatalf("resource calls = %d, want 2 (original + one retry)", rig.fake.count("/_api/web"))
	}
	if rig.fake.last("/_api/web").bearer != "AT-2" {
		t.Fatalf("retry used %q, want the renewed AT-2", rig.fake.last("/_api/web").bearer)
	}
	if rig.session.Status().State != StateDead || rig.dead.Load() != 1 {
		t.Fatalf("state=%q dead=%d, want dead after repeated 401", rig.session.Status().State, rig.dead.Load())
	}
}

func TestDoRecoversWhenTheRetrySucceeds(t *testing.T) {
	rig := newSessionRig(t, "RT-0")
	rig.fake.on("/token",
		reply{status: 200, body: tokenJSON("AT-1", "RT-1", 3600)},
		reply{status: 200, body: tokenJSON("AT-2", "RT-2", 3600)},
	)
	rig.fake.on("/_api/web", reply{status: 401}, reply{status: 200, body: "{}"})

	req, _ := http.NewRequest(http.MethodGet, "https://contoso.sharepoint.com/_api/web", nil)
	resp, err := rig.session.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("resp=%v err=%v", resp, err)
	}
	resp.Body.Close()
	if rig.session.Status().State != StateActive {
		t.Fatal("a single 401 recovered by renewal must not kill the session")
	}
}

func TestStatusNeverExposesTokens(t *testing.T) {
	rig := newSessionRig(t, "RT-0")
	rig.fake.on("/token", reply{status: 200, body: tokenJSON("AT-SECRET", "RT-SECRET", 3600)})
	_, _ = rig.session.Token(context.Background())

	st := rig.session.Status()

	if st.State != StateActive || st.ExpiresAt.IsZero() {
		t.Fatalf("status = %+v", st)
	}
	// Status has no token field at all; this guards against one being added.
	if text := fmtStatus(st); containsAny(text, "AT-SECRET", "RT-SECRET") {
		t.Fatalf("status leaks a token: %s", text)
	}
}

func fmtStatus(st Status) string {
	raw, _ := json.Marshal(st)
	return string(raw) + fmt.Sprintf("%+v", st)
}

func containsAny(haystack string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}
