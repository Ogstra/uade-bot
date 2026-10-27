package entra

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Manager gives every Discord user their own Microsoft session. Nobody shares
// a token: each user signs in with their own account, their refresh token is
// encrypted in a file of its own (bound to their id, see Store.Subject), and a
// session dying affects only its owner.
type Manager struct {
	Config   Config
	HTTP     Doer
	Dir      string
	Secret   []byte
	Validate Validator
	Now      func() time.Time
	Sleep    Sleeper
	Tick     time.Duration

	// OnDead fires once when a user's session dies, so they can be told to
	// sign in again. OnLoginFinish reports the end of a sign-in to its user.
	OnDead        func(userID string)
	OnLoginFinish func(userID string, status LoginStatus)

	mu       sync.Mutex
	sessions map[string]*Session
	logins   map[string]*Login
}

// ErrInvalidUser rejects anything that is not a Discord snowflake. The id
// becomes a filename, so this is also the path-traversal guard.
var ErrInvalidUser = errors.New("entra: invalid user id")

var snowflakePattern = regexp.MustCompile(`^[0-9]{15,25}$`)

func (m *Manager) client() *Client {
	return &Client{Config: m.Config, HTTP: m.HTTP, Now: m.Now}
}

func (m *Manager) store(userID string) *Store {
	return &Store{Path: filepath.Join(m.Dir, userID+".json"), Secret: m.Secret, Subject: userID}
}

// Session returns userID's session, loading it from disk the first time.
func (m *Manager) Session(userID string) (*Session, error) {
	if !snowflakePattern.MatchString(userID) {
		return nil, ErrInvalidUser
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions == nil {
		m.sessions = map[string]*Session{}
	}
	if s, ok := m.sessions[userID]; ok {
		return s, nil
	}
	s := &Session{
		Client:   m.client(),
		Store:    m.store(userID),
		Validate: m.Validate,
		Now:      m.Now,
		Tick:     m.Tick,
		OnDead: func() {
			if m.OnDead != nil {
				m.OnDead(userID)
			}
		},
	}
	if err := s.Load(); err != nil {
		// Unreadable file: start without a session rather than refuse the
		// user. They sign in again and the bad file is overwritten.
		log.Printf("entra: stored session unreadable, starting signed out")
	}
	m.sessions[userID] = s
	return s, nil
}

// BeginLogin starts (or returns the pending) device-code sign-in for userID.
// Each user has their own: one person's pending code never blocks or replaces
// another's.
func (m *Manager) BeginLogin(ctx context.Context, userID string) (LoginStatus, error) {
	session, err := m.Session(userID)
	if err != nil {
		return LoginStatus{}, err
	}
	m.mu.Lock()
	if m.logins == nil {
		m.logins = map[string]*Login{}
	}
	login, ok := m.logins[userID]
	if !ok {
		login = &Login{
			Client:  m.client(),
			Session: session,
			Sleep:   m.Sleep,
			Now:     m.Now,
			OnFinish: func(requester string, status LoginStatus) {
				if m.OnLoginFinish != nil {
					m.OnLoginFinish(requester, status)
				}
			},
		}
		m.logins[userID] = login
	}
	m.mu.Unlock()
	return login.Begin(ctx, userID)
}

func (m *Manager) LoginStatus(userID string) LoginStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	if login, ok := m.logins[userID]; ok {
		return login.Status()
	}
	return LoginStatus{State: LoginIdle}
}

// Forget signs userID out: drops the in-memory tokens and deletes the file.
func (m *Manager) Forget(userID string) error {
	if !snowflakePattern.MatchString(userID) {
		return ErrInvalidUser
	}
	m.mu.Lock()
	delete(m.sessions, userID)
	delete(m.logins, userID)
	m.mu.Unlock()
	return m.store(userID).Clear()
}

// LoadAll brings every stored session into memory at startup. Without it, a
// user who never touches the bot would never have their token renewed, and
// Microsoft retires refresh tokens left unused for long enough.
func (m *Manager) LoadAll() error {
	entries, err := os.ReadDir(m.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		if _, err := m.Session(strings.TrimSuffix(name, ".json")); err != nil {
			continue // not a session file; ignore rather than fail startup
		}
	}
	return nil
}

func (m *Manager) active() []*Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		if s.Status().State == StateActive {
			out = append(out, s)
		}
	}
	return out
}

// Run renews every active session in the background. One ticker drives all of
// them; each session still renews single-flight on its own.
func (m *Manager) Run(ctx context.Context) {
	tick := m.Tick
	if tick <= 0 {
		tick = DefaultTick
	}
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.RenewAll(ctx)
		}
	}
}

// RenewAll is one tick of Run, exposed so tests drive it directly.
func (m *Manager) RenewAll(ctx context.Context) {
	for _, s := range m.active() {
		_ = s.EnsureFresh(ctx, DefaultRenewBefore)
	}
}

// Status reports userID's session without loading secrets into the answer.
func (m *Manager) Status(userID string) (Status, error) {
	session, err := m.Session(userID)
	if err != nil {
		return Status{}, err
	}
	return session.Status(), nil
}
