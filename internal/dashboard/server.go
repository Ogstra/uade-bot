package dashboard

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	developmentCookie = "uade_dashboard"
	productionCookie  = "__Host-uade_dashboard"
	sessionCookie     = developmentCookie
	defaultSessionTTL = 8 * time.Hour
)

type session struct {
	CSRF          string
	ExpiresAt     time.Time
	Authenticated bool
	CreatedAt     time.Time
}

type loginAttempt struct {
	WindowStart time.Time
	Failures    int
}

type Server struct {
	User, Password string
	SessionSecret  []byte
	Snapshot       SnapshotFunc
	Now            func() time.Time
	Random         io.Reader
	Production     bool
	SessionTTL     time.Duration
	MaxSessions    int
	LoginLimit     int
	LoginWindow    time.Duration

	mu       sync.Mutex
	sessions map[string]session
	attempts map[string]loginAttempt
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	nonce, err := randomTokenN(s.random(), 16)
	if err != nil {
		http.Error(w, "Error interno.", http.StatusInternalServerError)
		return
	}
	s.securityHeaders(w, nonce)
	if r.URL.Path == "/healthz" {
		writeJSON(w, http.StatusOK, map[string]any{"generatedAt": s.now().UnixMilli(), "status": "ok"})
		return
	}
	switch {
	case r.URL.Path == "/login" && r.Method == http.MethodGet:
		s.loginPage(w, r, nonce, "", http.StatusOK)
	case r.URL.Path == "/login" && r.Method == http.MethodPost:
		s.login(w, r, nonce)
	case r.URL.Path == "/logout" && r.Method == http.MethodPost:
		s.logout(w, r)
	case r.URL.Path == "/dashboard" && r.Method == http.MethodGet:
		s.dashboard(w, r, nonce)
	case r.URL.Path == "/api/dashboard" && r.Method == http.MethodGet:
		s.dashboardJSON(w, r)
	case r.URL.Path == "/" && r.Method == http.MethodGet:
		http.Redirect(w, r, "/dashboard", http.StatusFound)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
func (s *Server) random() io.Reader {
	if s.Random != nil {
		return s.Random
	}
	return rand.Reader
}
func (s *Server) ttl() time.Duration {
	if s.SessionTTL > 0 {
		return s.SessionTTL
	}
	return defaultSessionTTL
}
func (s *Server) maxSessions() int {
	if s.MaxSessions > 0 {
		return s.MaxSessions
	}
	return 1024
}
func (s *Server) loginLimit() int {
	if s.LoginLimit > 0 {
		return s.LoginLimit
	}
	return 5
}
func (s *Server) loginWindow() time.Duration {
	if s.LoginWindow > 0 {
		return s.LoginWindow
	}
	return 15 * time.Minute
}
func (s *Server) cookieName() string {
	if s.Production {
		return productionCookie
	}
	return developmentCookie
}

func (s *Server) snapshot() (Snapshot, error) {
	if s.Snapshot != nil {
		return s.Snapshot()
	}
	return Snapshot{GeneratedAt: s.now().UnixMilli(), BotGuilds: []Guild{}, Accounts: []Account{}}, nil
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request, nonce, message string, status int) {
	previous, _ := s.sessionID(r)
	sid, csrf, err := s.newSession(false, previous)
	if err != nil {
		http.Error(w, "Error interno.", http.StatusInternalServerError)
		return
	}
	s.setCookie(w, sid, int(s.ttl().Seconds()))
	s.renderLoginStatus(w, nonce, csrf, message, status)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request, nonce string) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Solicitud rechazada.", http.StatusBadRequest)
		return
	}
	sid, current, ok := s.currentSession(r)
	if !ok || current.Authenticated || !sameOrigin(r) || !constantEqual(r.Form.Get("_csrf"), current.CSRF) {
		http.Error(w, "Solicitud rechazada.", http.StatusForbidden)
		return
	}
	if s.rateLimited(clientKey(r)) {
		w.Header().Set("Retry-After", "900")
		http.Error(w, "Demasiados intentos. Esperá unos minutos y volvé a intentar.", http.StatusTooManyRequests)
		return
	}
	if !constantEqual(r.Form.Get("username"), s.User) || !constantEqual(r.Form.Get("password"), s.Password) {
		s.recordFailure(clientKey(r))
		s.renderLoginStatus(w, nonce, current.CSRF, "Usuario o contraseña incorrectos. Volvé a intentarlo.", http.StatusUnauthorized)
		return
	}
	s.clearFailures(clientKey(r))
	newSID, _, err := s.newSession(true, sid)
	if err != nil {
		http.Error(w, "Error interno.", http.StatusInternalServerError)
		return
	}
	s.setCookie(w, newSID, int(s.ttl().Seconds()))
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	sid, current, ok := s.authenticated(r)
	if !ok || !sameOrigin(r) || !constantEqual(r.FormValue("_csrf"), current.CSRF) {
		http.Error(w, "Solicitud rechazada.", http.StatusForbidden)
		return
	}
	s.mu.Lock()
	delete(s.sessions, sid)
	s.mu.Unlock()
	s.setCookie(w, "", -1)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request, nonce string) {
	_, current, ok := s.authenticated(r)
	if !ok {
		s.expireCookie(w)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	snapshot, err := s.snapshot()
	if err != nil {
		s.renderError(w, nonce)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	noStore(w)
	if err := dashboardTemplate.Execute(w, dashboardView{Snapshot: snapshot, CSRF: current.CSRF, Nonce: nonce}); err != nil {
		return
	}
}

func (s *Server) dashboardJSON(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.authenticated(r); !ok {
		s.expireCookie(w)
		noStore(w)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	snapshot, err := s.snapshot()
	if err != nil {
		noStore(w)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}
	noStore(w)
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) renderLoginStatus(w http.ResponseWriter, nonce, csrf, message string, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	noStore(w)
	w.WriteHeader(status)
	_ = loginTemplate.Execute(w, loginView{Nonce: nonce, CSRF: csrf, Message: message})
}

func (s *Server) renderError(w http.ResponseWriter, nonce string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	noStore(w)
	w.WriteHeader(http.StatusInternalServerError)
	_ = errorTemplate.Execute(w, nonce)
}

func (s *Server) newSession(authenticated bool, previous string) (string, string, error) {
	sid, err := randomTokenN(s.random(), 32)
	if err != nil {
		return "", "", err
	}
	csrf, err := randomTokenN(s.random(), 32)
	if err != nil {
		return "", "", err
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		s.sessions = make(map[string]session)
	}
	delete(s.sessions, previous)
	s.cleanupLocked(now)
	for len(s.sessions) >= s.maxSessions() {
		s.evictOldestLocked()
	}
	s.sessions[sid] = session{CSRF: csrf, ExpiresAt: now.Add(s.ttl()), Authenticated: authenticated, CreatedAt: now}
	return sid, csrf, nil
}

func (s *Server) currentSession(r *http.Request) (string, session, bool) {
	sid, ok := s.sessionID(r)
	if !ok {
		return "", session{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked(s.now())
	current, ok := s.sessions[sid]
	return sid, current, ok
}
func (s *Server) authenticated(r *http.Request) (string, session, bool) {
	sid, current, ok := s.currentSession(r)
	return sid, current, ok && current.Authenticated
}
func (s *Server) sessionID(r *http.Request) (string, bool) {
	c, err := r.Cookie(s.cookieName())
	if err != nil {
		return "", false
	}
	return s.verify(c.Value)
}
func (s *Server) cleanupLocked(now time.Time) {
	for k, v := range s.sessions {
		if !v.ExpiresAt.After(now) {
			delete(s.sessions, k)
		}
	}
}
func (s *Server) evictOldestLocked() {
	var key string
	var at time.Time
	for k, v := range s.sessions {
		if key == "" || v.CreatedAt.Before(at) {
			key, at = k, v.CreatedAt
		}
	}
	delete(s.sessions, key)
}

func (s *Server) sign(sid string) string {
	mac := hmac.New(sha256.New, s.SessionSecret)
	_, _ = mac.Write([]byte(sid))
	return sid + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (s *Server) verify(value string) (string, bool) {
	sid, sig, ok := strings.Cut(value, ".")
	if !ok || sid == "" || sig == "" || len(s.SessionSecret) < 32 {
		return "", false
	}
	expected := s.sign(sid)
	return sid, hmac.Equal([]byte(expected), []byte(value))
}
func (s *Server) setCookie(w http.ResponseWriter, sid string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: s.cookieName(), Value: func() string {
		if sid == "" {
			return ""
		}
		return s.sign(sid)
	}(), Path: "/", HttpOnly: true, Secure: s.Production, SameSite: http.SameSiteLaxMode, MaxAge: maxAge})
}
func (s *Server) expireCookie(w http.ResponseWriter) { s.setCookie(w, "", -1) }

func (s *Server) rateLimited(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	a := s.attempts[key]
	return a.Failures >= s.loginLimit() && now.Sub(a.WindowStart) < s.loginWindow()
}
func (s *Server) recordFailure(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.attempts == nil {
		s.attempts = make(map[string]loginAttempt)
	}
	now := s.now()
	a := s.attempts[key]
	if a.WindowStart.IsZero() || now.Sub(a.WindowStart) >= s.loginWindow() {
		a = loginAttempt{WindowStart: now}
	}
	a.Failures++
	s.attempts[key] = a
}
func (s *Server) clearFailures(key string) { s.mu.Lock(); delete(s.attempts, key); s.mu.Unlock() }

func (s *Server) securityHeaders(w http.ResponseWriter, nonce string) {
	w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' https://cdn.discordapp.com; script-src 'self' 'nonce-"+nonce+"'; style-src 'self' 'nonce-"+nonce+"'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
}

func constantEqual(a, b string) bool {
	x := sha256.Sum256([]byte(a))
	y := sha256.Sum256([]byte(b))
	return hmac.Equal(x[:], y[:])
}
func randomTokenN(reader io.Reader, n int) (string, error) {
	v := make([]byte, n)
	if _, err := io.ReadFull(reader, v); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(v), nil
}
func sameOrigin(r *http.Request) bool {
	site := r.Header.Get("Sec-Fetch-Site")
	if site != "" && site != "same-origin" && site != "none" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == r.Host && (u.Scheme == "http" || u.Scheme == "https")
}
func clientKey(r *http.Request) string {
	host := r.RemoteAddr
	if parsed, _, ok := strings.Cut(host, ":"); ok {
		return parsed
	}
	return host
}
func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
