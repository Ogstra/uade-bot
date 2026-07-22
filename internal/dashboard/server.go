package dashboard

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const sessionCookie = "uade_dashboard"

// Snapshot is intentionally small in this plan. Plan 03.3-06 owns the full
// account/job/history projection while this establishes the SSR/JSON contract.
type Snapshot struct {
	GeneratedAt int64  `json:"generatedAt"`
	Status      string `json:"status"`
}

type SnapshotFunc func() (Snapshot, error)

type session struct {
	CSRF      string
	ExpiresAt time.Time
}

type Server struct {
	User, Password string
	SessionSecret  []byte
	Snapshot       SnapshotFunc
	Now            func() time.Time
	Random         io.Reader

	mu       sync.Mutex
	sessions map[string]session
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.securityHeaders(w)
	if r.URL.Path == "/healthz" {
		writeJSON(w, http.StatusOK, Snapshot{GeneratedAt: s.now().UnixMilli(), Status: "ok"})
		return
	}
	switch {
	case r.URL.Path == "/login" && r.Method == http.MethodGet:
		s.renderLogin(w, "")
	case r.URL.Path == "/login" && r.Method == http.MethodPost:
		s.login(w, r)
	case r.URL.Path == "/logout" && r.Method == http.MethodPost:
		s.logout(w, r)
	case r.URL.Path == "/dashboard" && r.Method == http.MethodGet:
		s.dashboard(w, r)
	case r.URL.Path == "/api/dashboard" && r.Method == http.MethodGet:
		s.dashboardJSON(w, r)
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

func (s *Server) snapshot() (Snapshot, error) {
	if s.Snapshot != nil {
		return s.Snapshot()
	}
	return Snapshot{GeneratedAt: s.now().UnixMilli(), Status: "ok"}, nil
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Solicitud rechazada.", http.StatusBadRequest)
		return
	}
	if !constantEqual(r.Form.Get("username"), s.User) || !constantEqual(r.Form.Get("password"), s.Password) {
		s.renderLoginStatus(w, "Usuario o contraseña incorrectos. Volvé a intentarlo.", http.StatusUnauthorized)
		return
	}
	sid, csrf, err := s.newSession()
	if err != nil {
		http.Error(w, "Error interno.", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: s.sign(sid), Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 8 * 60 * 60})
	_ = csrf
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
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	_, current, ok := s.authenticated(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	snapshot, err := s.snapshot()
	if err != nil {
		http.Error(w, "Error interno.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = dashboardTemplate.Execute(w, struct {
		Snapshot Snapshot
		CSRF     string
	}{snapshot, current.CSRF})
}

func (s *Server) dashboardJSON(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.authenticated(r); !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	snapshot, err := s.snapshot()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) renderLogin(w http.ResponseWriter, message string) {
	s.renderLoginStatus(w, message, http.StatusOK)
}
func (s *Server) renderLoginStatus(w http.ResponseWriter, message string, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = loginTemplate.Execute(w, message)
}

func (s *Server) newSession() (string, string, error) {
	sid, err := randomToken(s.random())
	if err != nil {
		return "", "", err
	}
	csrf, err := randomToken(s.random())
	if err != nil {
		return "", "", err
	}
	s.mu.Lock()
	if s.sessions == nil {
		s.sessions = make(map[string]session)
	}
	now := s.now()
	for key, value := range s.sessions {
		if !value.ExpiresAt.After(now) {
			delete(s.sessions, key)
		}
	}
	s.sessions[sid] = session{CSRF: csrf, ExpiresAt: now.Add(8 * time.Hour)}
	s.mu.Unlock()
	return sid, csrf, nil
}

func (s *Server) authenticated(r *http.Request) (string, session, bool) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return "", session{}, false
	}
	sid, ok := s.verify(cookie.Value)
	if !ok {
		return "", session{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.sessions[sid]
	if !ok || !current.ExpiresAt.After(s.now()) {
		delete(s.sessions, sid)
		return "", session{}, false
	}
	return sid, current, true
}

func (s *Server) sign(sid string) string {
	mac := hmac.New(sha256.New, s.SessionSecret)
	_, _ = mac.Write([]byte(sid))
	return sid + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Server) verify(value string) (string, bool) {
	sid, signature, ok := strings.Cut(value, ".")
	if !ok || sid == "" || len(s.SessionSecret) < 32 {
		return "", false
	}
	return sid, hmac.Equal([]byte(s.sign(sid)), []byte(value)) && signature != ""
}

func (s *Server) securityHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
}

func constantEqual(candidate, expected string) bool {
	a := sha256.Sum256([]byte(candidate))
	b := sha256.Sum256([]byte(expected))
	return hmac.Equal(a[:], b[:])
}

func randomToken(reader io.Reader) (string, error) {
	value := make([]byte, 32)
	if _, err := io.ReadFull(reader, value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil && parsed.Host == r.Host && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

var loginTemplate = template.Must(template.New("login").Parse(`<!doctype html><html lang="es"><head><meta charset="utf-8"><title>UADE Bot</title></head><body><main><h1>Dashboard de UADE Bot</h1>{{if .}}<p role="alert">{{.}}</p>{{end}}<form method="post" action="/login"><label>Usuario <input name="username" autocomplete="username" required></label><label>Contraseña <input type="password" name="password" autocomplete="current-password" required></label><button>Iniciar sesión</button></form></main></body></html>`))

var dashboardTemplate = template.Must(template.New("dashboard").Parse(`<!doctype html><html lang="es"><head><meta charset="utf-8"><title>Estado | UADE Bot</title></head><body><header><h1>Estado del sistema</h1><form method="post" action="/logout"><input type="hidden" name="_csrf" value="{{.CSRF}}"><button>Cerrar sesión</button></form></header><main id="dashboard" data-generated-at="{{.Snapshot.GeneratedAt}}"><p id="status">{{.Snapshot.Status}}</p></main><script>async function refresh(){const r=await fetch('/api/dashboard',{headers:{accept:'application/json'}});if(r.status===401){location.assign('/login');return}if(r.ok){const x=await r.json();document.querySelector('#status').textContent=x.status;document.querySelector('#dashboard').dataset.generatedAt=x.generatedAt}}setInterval(refresh,10000)</script></body></html>`))
