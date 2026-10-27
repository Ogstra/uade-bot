package entra

import (
	"context"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"
)

type State string

const (
	// StateNone: nobody has signed in yet, or the stored token was unreadable.
	StateNone State = "none"
	// StateActive: a refresh token is held; the access token may be renewing.
	StateActive State = "active"
	// StateDead: the session was active and Microsoft has since rejected it
	// for good. Only a new device-code sign-in revives it.
	StateDead State = "dead"
)

const (
	// DefaultRenewBefore renews well ahead of expiry so a long task that
	// starts with a "fresh" token does not see it expire halfway through.
	DefaultRenewBefore = 25 * time.Minute
	// DefaultTick is the background renewal cadence. It is shorter than
	// DefaultRenewBefore, so at least one tick always lands inside the
	// renewal window of every access token.
	DefaultTick = 20 * time.Minute
)

// ErrNoSession is returned by Token when there is no usable access token.
var ErrNoSession = errors.New("entra: no active session")

// Validator proves a token works by making one real request to the resource.
// A token is never adopted on Microsoft's word alone: the token endpoint can
// hand out a token the resource then refuses (missing consent, Conditional
// Access, wrong audience).
type Validator func(ctx context.Context, accessToken string) error

type Status struct {
	State     State     `json:"state"`
	ExpiresAt time.Time `json:"expiresAt,omitempty"`
}

// Session owns the single Microsoft session of the process.
//
// The access token lives only here, in memory. Callers never hold it: they ask
// Token for the current one on every request, so a token renewed in the
// middle of a long task is picked up by the very next request instead of the
// task running to completion on a stale copy.
type Session struct {
	Client      *Client
	Store       *Store
	Validate    Validator
	OnDead      func()
	Now         func() time.Time
	RenewBefore time.Duration
	Tick        time.Duration

	mu      sync.Mutex
	access  string
	expires time.Time
	refresh string
	state   State
	flight  *flight
}

type flight struct {
	done chan struct{}
	err  error
}

func (s *Session) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Session) renewBefore() time.Duration {
	if s.RenewBefore > 0 {
		return s.RenewBefore
	}
	return DefaultRenewBefore
}

func (s *Session) tick() time.Duration {
	if s.Tick > 0 {
		return s.Tick
	}
	return DefaultTick
}

// Load restores the stored refresh token at startup. No access token exists
// yet; the first Token call (or the first tick) obtains one.
func (s *Session) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	refresh, err := s.Store.Load()
	switch {
	case err == nil:
		s.refresh, s.state = refresh, StateActive
		return nil
	case errors.Is(err, ErrNoToken):
		s.state = StateNone
		return nil
	default:
		// Unreadable is not "dead": nothing was rejected by Microsoft, there
		// is simply no trustworthy token. Fail closed and wait for sign-in.
		s.state = StateNone
		return err
	}
}

func (s *Session) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.state
	if state == "" {
		state = StateNone
	}
	st := Status{State: state}
	if s.access != "" {
		st.ExpiresAt = s.expires
	}
	return st
}

// Token is the getter every outgoing request goes through.
func (s *Session) Token(ctx context.Context) (string, error) {
	return s.tokenWithin(ctx, s.renewBefore())
}

// EnsureFresh renews now if the current token would not survive horizon.
// Call it before a long task whose duration is known.
func (s *Session) EnsureFresh(ctx context.Context, horizon time.Duration) error {
	if horizon < s.renewBefore() {
		horizon = s.renewBefore()
	}
	_, err := s.tokenWithin(ctx, horizon)
	return err
}

func (s *Session) tokenWithin(ctx context.Context, horizon time.Duration) (string, error) {
	s.mu.Lock()
	if s.access != "" && s.now().Add(horizon).Before(s.expires) {
		token := s.access
		s.mu.Unlock()
		return token, nil
	}
	s.mu.Unlock()

	renewErr := s.renew(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()
	// After a transient failure the old token may still have minutes left:
	// use it rather than failing a request the token could still serve.
	if s.access != "" && s.now().Before(s.expires) {
		return s.access, nil
	}
	if renewErr != nil {
		return "", renewErr
	}
	return "", ErrNoSession
}

// renew is single-flight: any number of concurrent callers share exactly one
// request to Microsoft. Besides saving round-trips this is a correctness
// requirement -- Microsoft rotates refresh tokens, so two parallel refreshes
// with the same token race each other and the loser can invalidate the
// session the winner just obtained.
func (s *Session) renew(ctx context.Context) error {
	s.mu.Lock()
	if f := s.flight; f != nil {
		s.mu.Unlock()
		select {
		case <-f.done:
			return f.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if s.refresh == "" {
		s.mu.Unlock()
		return ErrNoSession
	}
	f := &flight{done: make(chan struct{})}
	s.flight = f
	refresh := s.refresh
	s.mu.Unlock()

	// The shared request must not die with whichever caller happened to start
	// it: other callers are waiting on the same result.
	f.err = s.refreshOnce(context.WithoutCancel(ctx), refresh)

	s.mu.Lock()
	s.flight = nil
	s.mu.Unlock()
	close(f.done)
	return f.err
}

func (s *Session) refreshOnce(ctx context.Context, refresh string) error {
	tokens, err := s.Client.Refresh(ctx, refresh)
	if err != nil {
		if IsPermanent(err) {
			s.markDead()
		}
		// Transient: keep whatever access token is still valid and let the
		// next tick (or the next request) try again.
		return err
	}
	// Rotation: Microsoft normally returns a new refresh token and the old one
	// stops working. When it returns none, the old one stays valid.
	if tokens.RefreshToken == "" {
		tokens.RefreshToken = refresh
	}
	if err = s.validate(ctx, tokens.AccessToken); err != nil {
		// Do not swap a working session for one the resource refuses. The
		// rotated refresh token is still kept below though: the old one may
		// already be invalidated by the rotation, and discarding the new one
		// would lose the session entirely.
		s.mu.Lock()
		s.refresh = tokens.RefreshToken
		s.mu.Unlock()
		s.persist(tokens.RefreshToken)
		return &TransientError{}
	}
	s.mu.Lock()
	s.access, s.expires, s.refresh, s.state = tokens.AccessToken, tokens.ExpiresAt, tokens.RefreshToken, StateActive
	s.mu.Unlock()
	s.persist(tokens.RefreshToken)
	return nil
}

func (s *Session) validate(ctx context.Context, token string) error {
	if s.Validate == nil {
		return nil
	}
	return s.Validate(ctx, token)
}

func (s *Session) persist(refresh string) {
	if s.Store == nil {
		return
	}
	if err := s.Store.Save(refresh); err != nil {
		// The token is still held in memory, so the session keeps working
		// until the next restart. Never log the token, only the fact.
		log.Printf("entra: could not persist refresh token")
	}
}

// Adopt installs tokens from a completed device-code sign-in. They are
// validated against the resource first; on failure the current session, if
// any, is left exactly as it was.
func (s *Session) Adopt(ctx context.Context, tokens Tokens) error {
	if tokens.RefreshToken == "" {
		// Without offline_access consent there is no refresh token, and a
		// session that cannot renew dies within the hour.
		return &PermanentError{Code: "missing_refresh_token"}
	}
	if err := s.validate(ctx, tokens.AccessToken); err != nil {
		// Permanent for this sign-in, not for the session: Adopt never calls
		// markDead, so an existing working session survives a bad new one.
		return &PermanentError{Code: "validation_failed"}
	}
	s.mu.Lock()
	s.access, s.expires, s.refresh, s.state = tokens.AccessToken, tokens.ExpiresAt, tokens.RefreshToken, StateActive
	s.mu.Unlock()
	s.persist(tokens.RefreshToken)
	return nil
}

// markDead drops every secret and fires OnDead exactly once per transition,
// so a dead session produces one notification -- not one per request that
// happens to find it dead.
func (s *Session) markDead() {
	s.mu.Lock()
	wasDead := s.state == StateDead
	s.access, s.refresh, s.expires, s.state = "", "", time.Time{}, StateDead
	s.mu.Unlock()
	if s.Store != nil {
		if err := s.Store.Clear(); err != nil {
			log.Printf("entra: could not clear dead refresh token")
		}
	}
	if !wasDead && s.OnDead != nil {
		s.OnDead()
	}
}

// Run renews in the background until ctx ends.
func (s *Session) Run(ctx context.Context) {
	ticker := time.NewTicker(s.tick())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if s.Status().State == StateActive {
				_ = s.EnsureFresh(ctx, s.renewBefore())
			}
		}
	}
}

// Do sends req with the current Bearer token. A 401 under Bearer means the
// resource no longer accepts the session (with cookies SharePoint would
// redirect to the login page instead). It gets one forced renewal and one
// retry; a second 401 kills the session.
func (s *Session) Do(req *http.Request) (*http.Response, error) {
	token, err := s.Token(req.Context())
	if err != nil {
		return nil, err
	}
	resp, err := s.send(req, token)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		return resp, err
	}
	resp.Body.Close()

	s.mu.Lock()
	if s.access == token {
		s.access = "" // force the renewal below instead of reusing it
	}
	s.mu.Unlock()
	token, err = s.Token(req.Context())
	if err != nil {
		return nil, err
	}
	retry, ok := replayable(req)
	if !ok {
		return nil, ErrNoSession
	}
	resp, err = s.send(retry, token)
	if err == nil && resp.StatusCode == http.StatusUnauthorized {
		s.markDead()
	}
	return resp, err
}

func (s *Session) send(req *http.Request, token string) (*http.Response, error) {
	out := req.Clone(req.Context())
	out.Header.Set("Authorization", "Bearer "+token)
	return s.Client.doer().Do(out)
}

func replayable(req *http.Request) (*http.Request, bool) {
	if req.Body == nil || req.Body == http.NoBody {
		return req, true
	}
	if req.GetBody == nil {
		return nil, false
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, false
	}
	out := req.Clone(req.Context())
	out.Body = body
	return out, true
}
