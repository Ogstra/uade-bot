package dashboard

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

var csrfPattern = regexp.MustCompile(`name="_csrf" value="([^"]+)"`)

func authenticatedClient(t *testing.T, server *httptest.Server, user, password string) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	response, err := client.Get(server.URL + "/login")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	match := csrfPattern.FindSubmatch(body)
	if len(match) != 2 {
		t.Fatalf("login csrf missing: %s", body)
	}
	response, err = client.PostForm(server.URL+"/login", url.Values{"username": {user}, "password": {password}, "_csrf": {string(match[1])}})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.Request.URL.Path != "/dashboard" {
		t.Fatalf("got %s", response.Request.URL.Path)
	}
	return client
}

func TestServerAuthenticatedSSRJSONAndLogoutFlow(t *testing.T) {
	now := time.Unix(1_750_000_000, 0)
	s := &Server{User: "admin", Password: "secret", SessionSecret: []byte("0123456789abcdef0123456789abcdef"), Now: func() time.Time { return now }, Snapshot: func() (Snapshot, error) {
		return Snapshot{GeneratedAt: now.UnixMilli(), BotGuilds: []Guild{}, Accounts: []Account{}, Health: Health{PausedAccounts: PausedHealth{Breakdown: []Breakdown{}}}}, nil
	}}
	httpServer := httptest.NewServer(s)
	defer httpServer.Close()
	client := authenticatedClient(t, httpServer, "admin", "secret")
	dashboard, err := client.Get(httpServer.URL + "/dashboard")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(dashboard.Body)
	dashboard.Body.Close()
	html := string(body)
	for _, want := range []string{"Resumen de salud", "Cuentas y búsquedas", "/api/dashboard", "setInterval", "name=\"_csrf\""} {
		if !strings.Contains(html, want) {
			t.Errorf("SSR missing %q", want)
		}
	}
	if strings.Contains(dashboard.Header.Get("Content-Security-Policy"), "unsafe-inline") {
		t.Fatal("CSP must use nonces")
	}
	api, _ := client.Get(httpServer.URL + "/api/dashboard")
	apiBody, _ := io.ReadAll(api.Body)
	api.Body.Close()
	if api.StatusCode != 200 || !strings.Contains(string(apiBody), `"accounts":[]`) {
		t.Fatalf("api=%d %s", api.StatusCode, apiBody)
	}
	match := csrfPattern.FindStringSubmatch(html)
	logout, _ := client.PostForm(httpServer.URL+"/logout", url.Values{"_csrf": {match[1]}})
	logout.Body.Close()
	if logout.Request.URL.Path != "/login" {
		t.Fatal(logout.Request.URL.Path)
	}
	unauthorized, _ := client.Get(httpServer.URL + "/api/dashboard")
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatal(unauthorized.StatusCode)
	}
	unauthorized.Body.Close()
}

func TestLoginRequiresCSRFAndRateLimitsFailures(t *testing.T) {
	s := &Server{User: "a", Password: "b", SessionSecret: []byte("0123456789abcdef0123456789abcdef"), LoginLimit: 2}
	server := httptest.NewServer(s)
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	bad, _ := client.PostForm(server.URL+"/login", url.Values{"username": {"a"}, "password": {"b"}})
	if bad.StatusCode != http.StatusForbidden {
		t.Fatalf("csrf=%d", bad.StatusCode)
	}
	bad.Body.Close()
	login, _ := client.Get(server.URL + "/login")
	body, _ := io.ReadAll(login.Body)
	login.Body.Close()
	csrf := string(csrfPattern.FindSubmatch(body)[1])
	for _, want := range []int{401, 401, 429} {
		response, _ := client.PostForm(server.URL+"/login", url.Values{"username": {"a"}, "password": {"wrong"}, "_csrf": {csrf}})
		if response.StatusCode != want {
			t.Fatalf("want %d got %d", want, response.StatusCode)
		}
		response.Body.Close()
	}
}

func TestSessionsAreBoundedExpireAndProductionCookieIsHardened(t *testing.T) {
	now := time.Unix(100, 0)
	s := &Server{SessionSecret: []byte("0123456789abcdef0123456789abcdef"), Now: func() time.Time { return now }, MaxSessions: 2, SessionTTL: time.Minute, Production: true}
	for i := 0; i < 3; i++ {
		if _, _, err := s.newSession(false, ""); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.sessions) != 2 {
		t.Fatalf("sessions=%d", len(s.sessions))
	}
	now = now.Add(2 * time.Minute)
	s.mu.Lock()
	s.cleanupLocked(now)
	s.mu.Unlock()
	if len(s.sessions) != 0 {
		t.Fatal("expired session retained")
	}
	r := httptest.NewRequest(http.MethodGet, "https://example.test/login", nil)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	cookie := w.Header().Get("Set-Cookie")
	for _, want := range []string{"__Host-uade_dashboard=", "Secure", "HttpOnly", "SameSite=Lax"} {
		if !strings.Contains(cookie, want) {
			t.Errorf("cookie missing %s: %s", want, cookie)
		}
	}
}

func TestUnauthorizedTamperOriginAndSecurityHeaders(t *testing.T) {
	s := &Server{SessionSecret: []byte("0123456789abcdef0123456789abcdef")}
	r := httptest.NewRequest(http.MethodGet, "/api/dashboard", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "attacker.invalid"})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 401 || strings.Contains(w.Body.String(), "attacker") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	health := httptest.NewRecorder()
	s.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	for _, header := range []string{"Content-Security-Policy", "Referrer-Policy", "X-Content-Type-Options", "X-Frame-Options", "Permissions-Policy"} {
		if health.Header().Get(header) == "" {
			t.Errorf("missing %s", header)
		}
	}
}

func TestRootPathRedirectsToDashboard(t *testing.T) {
	s := &Server{SessionSecret: []byte("0123456789abcdef0123456789abcdef")}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/dashboard" {
		t.Fatalf("GET / = %d Location=%q, want %d Location=/dashboard", w.Code, w.Header().Get("Location"), http.StatusFound)
	}

	post := httptest.NewRecorder()
	s.ServeHTTP(post, httptest.NewRequest(http.MethodPost, "/", nil))
	if post.Code != http.StatusNotFound {
		t.Fatalf("POST / = %d, want %d", post.Code, http.StatusNotFound)
	}

	unknown := httptest.NewRecorder()
	s.ServeHTTP(unknown, httptest.NewRequest(http.MethodGet, "/some-unknown-path", nil))
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("GET /some-unknown-path = %d, want %d", unknown.Code, http.StatusNotFound)
	}
}

func TestHTMLContractSnapshotSharedWithNode(t *testing.T) {
	var contract struct{ Login, Dashboard, Forbidden []string }
	raw, err := os.ReadFile("testdata/html_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &contract); err != nil {
		t.Fatal(err)
	}
	s := &Server{SessionSecret: []byte("0123456789abcdef0123456789abcdef")}
	login := httptest.NewRecorder()
	s.ServeHTTP(login, httptest.NewRequest(http.MethodGet, "/login", nil))
	snapshot := Snapshot{GeneratedAt: 500, BotGuilds: []Guild{}, Accounts: []Account{}, Health: Health{PausedAccounts: PausedHealth{Breakdown: []Breakdown{}}}}
	s.Snapshot = func() (Snapshot, error) { return snapshot, nil }
	sid, _, _ := s.newSession(true, "")
	r := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: s.sign(sid)})
	dashboard := httptest.NewRecorder()
	s.ServeHTTP(dashboard, r)
	for _, pair := range []struct {
		body    string
		markers []string
	}{{login.Body.String(), contract.Login}, {dashboard.Body.String(), contract.Dashboard}} {
		for _, marker := range pair.markers {
			if !strings.Contains(pair.body, marker) {
				t.Errorf("missing shared marker %q", marker)
			}
		}
		for _, forbidden := range contract.Forbidden {
			if strings.Contains(pair.body, forbidden) {
				t.Errorf("render leaked %q", forbidden)
			}
		}
	}
}
