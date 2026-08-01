package dashboard

import (
	"bytes"
	"database/sql"
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

	"github.com/ogs/uade-bot/internal/store"
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

func TestServerRendersGuildIconAndAccountAvatarWhenPresent(t *testing.T) {
	iconURL := "https://cdn.discordapp.com/icons/guild-a/abc.png"
	avatarURL := "https://cdn.discordapp.com/avatars/user-a/def.png"
	s := &Server{User: "admin", Password: "secret", SessionSecret: []byte("0123456789abcdef0123456789abcdef"), Snapshot: func() (Snapshot, error) {
		return Snapshot{
			BotGuilds: []Guild{{ID: "guild-a", Name: "Servidor A", IconURL: iconURL}},
			Accounts:  []Account{{DiscordUserID: "user-a", DisplayName: "Ana", AvatarURL: avatarURL, Status: Status{Code: "active", Label: "Activa", Tone: "healthy"}, Jobs: []Job{}}},
			Health:    Health{PausedAccounts: PausedHealth{Breakdown: []Breakdown{}}},
		}, nil
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
	for _, want := range []string{`<img class="avatar" src="` + iconURL + `"`, `<img class="avatar" src="` + avatarURL + `"`} {
		if !strings.Contains(html, want) {
			t.Errorf("SSR missing %q: %s", want, html)
		}
	}
}

func TestServerOmitsImgEntirelyWhenIconAndAvatarURLsAreEmpty(t *testing.T) {
	s := &Server{User: "admin", Password: "secret", SessionSecret: []byte("0123456789abcdef0123456789abcdef"), Snapshot: func() (Snapshot, error) {
		return Snapshot{
			BotGuilds: []Guild{{ID: "guild-a", Name: "Servidor A", IconURL: ""}},
			Accounts:  []Account{{DiscordUserID: "user-a", DisplayName: "Ana", AvatarURL: "", Status: Status{Code: "active", Label: "Activa", Tone: "healthy"}, Jobs: []Job{}}},
			Health:    Health{PausedAccounts: PausedHealth{Breakdown: []Breakdown{}}},
		}, nil
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
	if strings.Contains(html, "<img") {
		t.Fatalf("expected no <img> tag when IconURL/AvatarURL are empty: %s", html)
	}
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

func TestSecurityHeadersIncludeDiscordCDNImgSrc(t *testing.T) {
	s := &Server{SessionSecret: []byte("0123456789abcdef0123456789abcdef")}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	csp := w.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "img-src 'self' https://cdn.discordapp.com") {
		t.Fatalf("CSP missing img-src for cdn.discordapp.com: %s", csp)
	}
	if !strings.Contains(csp, "object-src 'none'") {
		t.Fatalf("CSP missing object-src 'none': %s", csp)
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

func TestDashboardSSRUsesReadableBuenosAiresTimestampsAndFallbacks(t *testing.T) {
	fixed := time.Date(2026, 7, 30, 15, 4, 0, 0, time.UTC).UnixMilli()
	zero := int64(0)
	snapshot := Snapshot{
		GeneratedAt: fixed,
		Health:      Health{LastSuccessfulPollAt: &fixed, PausedAccounts: PausedHealth{Breakdown: []Breakdown{}}, Jobs: JobsHealth{}},
		BotGuilds:   []Guild{},
		Accounts: []Account{{DiscordUserID: "123", DisplayName: "Ana", PauseUntil: &fixed, LastPolledAt: &fixed, Jobs: []Job{{
			JobID: 7, LastPolledAt: &fixed, Filters: Filters{}, History: []HistoryItem{{ID: 9, RecordedAt: fixed}},
		}}}},
	}
	var rendered bytes.Buffer
	if err := dashboardTemplate.Execute(&rendered, dashboardView{Snapshot: snapshot}); err != nil {
		t.Fatal(err)
	}
	html := rendered.String()
	if got := strings.Count(html, "30/07/2026, 12:04"); got < 6 {
		t.Fatalf("formatted Buenos Aires timestamp count=%d, want at least 6: %s", got, html)
	}
	for _, marker := range []string{`data-field="generated-at"`, `data-field="health-last-success"`, `data-field="account-pause"`, `data-field="account-last-poll"`, `data-field="job-last-poll"`, `data-field="history-recorded-at"`} {
		if !strings.Contains(html, marker) {
			t.Errorf("missing %s", marker)
		}
	}

	snapshot.GeneratedAt = 0
	snapshot.Health.LastSuccessfulPollAt = nil
	snapshot.Accounts[0].PauseUntil = &zero
	snapshot.Accounts[0].LastPolledAt = nil
	snapshot.Accounts[0].Jobs[0].LastPolledAt = nil
	snapshot.Accounts[0].Jobs[0].History[0].RecordedAt = -1
	rendered.Reset()
	if err := dashboardTemplate.Execute(&rendered, dashboardView{Snapshot: snapshot}); err != nil {
		t.Fatal(err)
	}
	html = rendered.String()
	if strings.Contains(html, "01/01/1970") || strings.Contains(html, ">0</time>") || strings.Contains(html, ">-1</time>") {
		t.Fatalf("invalid epoch rendered visibly: %s", html)
	}
	for _, fallback := range []string{"—", "Sin polls exitosos", "Sin sondeos todavía"} {
		if !strings.Contains(html, fallback) {
			t.Errorf("missing fallback %q", fallback)
		}
	}
}

func TestDashboardSSRHidesZeroCountDetailForNonFoundOutcomes(t *testing.T) {
	fixed := time.Date(2026, 7, 30, 15, 4, 0, 0, time.UTC).UnixMilli()
	zero := 0
	one, cupos := 1, 4
	snapshot := Snapshot{
		GeneratedAt: fixed,
		Health:      Health{PausedAccounts: PausedHealth{Breakdown: []Breakdown{}}, Jobs: JobsHealth{}},
		BotGuilds:   []Guild{},
		Accounts: []Account{{DiscordUserID: "123", DisplayName: "Ana", Jobs: []Job{{
			JobID: 7, Filters: Filters{}, History: []HistoryItem{
				{ID: 9, RecordedAt: fixed, Outcome: outcomeCode("no_vacancies", &zero, &zero)},
				{ID: 8, RecordedAt: fixed, Outcome: outcomeCode("found", &one, &cupos)},
			},
		}}}},
	}
	var rendered bytes.Buffer
	if err := dashboardTemplate.Execute(&rendered, dashboardView{Snapshot: snapshot}); err != nil {
		t.Fatal(err)
	}
	html := rendered.String()
	if !strings.Contains(html, "—") {
		t.Fatalf("expected dash placeholder for non-found outcome: %s", html)
	}
	if strings.Contains(html, "0 comisión(es)") {
		t.Fatalf("no_vacancies row must not render zero-count detail: %s", html)
	}
	if !strings.Contains(html, "1 comisión(es), 4 cupo(s)") {
		t.Fatalf("found row must still render vacancy count detail: %s", html)
	}
}

func TestDashboardCSSStacksHealthCardAndAccountSummaryChildren(t *testing.T) {
	snapshot := Snapshot{
		BotGuilds: []Guild{},
		Accounts:  []Account{},
		Health:    Health{PausedAccounts: PausedHealth{Breakdown: []Breakdown{}}, Jobs: JobsHealth{}},
	}
	var rendered bytes.Buffer
	if err := dashboardTemplate.Execute(&rendered, dashboardView{Snapshot: snapshot}); err != nil {
		t.Fatal(err)
	}
	html := rendered.String()
	for _, want := range []string{
		".health-card{display:flex;flex-direction:column;gap:4px}",
		".account>summary{display:flex;align-items:center;flex-wrap:wrap;gap:8px}",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("rendered style block missing %q: %s", want, html)
		}
	}
}

func seedJobActionDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(t.TempDir() + "/dashboard.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err = db.Exec(`INSERT INTO users(discord_user_id,backoff_attempt,created_at,updated_at) VALUES ('user-a',0,1,1);
INSERT INTO jobs(id,discord_user_id,filtros_json,status,created_at) VALUES (1,'user-a','{}','active',1)`); err != nil {
		t.Fatal(err)
	}
	return db
}

func jobRowStatus(t *testing.T, db *sql.DB, id int64) (string, bool) {
	t.Helper()
	var status string
	err := db.QueryRow(`SELECT status FROM jobs WHERE id=?`, id).Scan(&status)
	if err == sql.ErrNoRows {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return status, true
}

func TestJobActionRequiresSessionSameOriginAndCSRF(t *testing.T) {
	db := seedJobActionDB(t)
	s := &Server{User: "admin", Password: "secret", SessionSecret: []byte("0123456789abcdef0123456789abcdef"), DB: db}
	httpServer := httptest.NewServer(s)
	defer httpServer.Close()

	assertUnchanged := func() {
		t.Helper()
		status, ok := jobRowStatus(t, db, 1)
		if !ok || status != "active" {
			t.Fatalf("job mutated despite rejected request: status=%q ok=%v", status, ok)
		}
	}

	// No cookie/session at all.
	noSession := &http.Client{}
	resp, err := noSession.PostForm(httpServer.URL+"/jobs/accion", url.Values{"id": {"1"}, "accion": {"pausar"}, "_csrf": {"whatever"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("no session status=%d", resp.StatusCode)
	}
	assertUnchanged()

	client := authenticatedClient(t, httpServer, "admin", "secret")

	// CSRF absent.
	resp, err = client.PostForm(httpServer.URL+"/jobs/accion", url.Values{"id": {"1"}, "accion": {"pausar"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("csrf absent status=%d", resp.StatusCode)
	}
	assertUnchanged()

	// CSRF present but incorrect.
	resp, err = client.PostForm(httpServer.URL+"/jobs/accion", url.Values{"id": {"1"}, "accion": {"pausar"}, "_csrf": {"not-the-real-token"}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("csrf wrong status=%d", resp.StatusCode)
	}
	assertUnchanged()

	// Session + correct CSRF, but Origin header is cross-site.
	dashboardResp, err := client.Get(httpServer.URL + "/dashboard")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(dashboardResp.Body)
	dashboardResp.Body.Close()
	match := csrfPattern.FindSubmatch(body)
	if len(match) != 2 {
		t.Fatalf("csrf missing: %s", body)
	}
	req, err := http.NewRequest(http.MethodPost, httpServer.URL+"/jobs/accion", strings.NewReader(url.Values{"id": {"1"}, "accion": {"pausar"}, "_csrf": {string(match[1])}}.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin status=%d", resp.StatusCode)
	}
	assertUnchanged()
}

func TestJobActionPausesResumesDeletesAndNotifiesScheduler(t *testing.T) {
	db := seedJobActionDB(t)
	var jobsChangedCount int
	var jobCreatedIDs []string
	s := &Server{
		User: "admin", Password: "secret", SessionSecret: []byte("0123456789abcdef0123456789abcdef"), DB: db,
		OnJobsChanged: func() { jobsChangedCount++ },
		OnJobCreated:  func(id string) { jobCreatedIDs = append(jobCreatedIDs, id) },
	}
	httpServer := httptest.NewServer(s)
	defer httpServer.Close()
	client := authenticatedClient(t, httpServer, "admin", "secret")
	noRedirect := &http.Client{Jar: client.Jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	dashboardResp, err := client.Get(httpServer.URL + "/dashboard")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(dashboardResp.Body)
	dashboardResp.Body.Close()
	csrf := string(csrfPattern.FindSubmatch(body)[1])

	post := func(accion string) *http.Response {
		t.Helper()
		resp, err := noRedirect.PostForm(httpServer.URL+"/jobs/accion", url.Values{"id": {"1"}, "accion": {accion}, "_csrf": {csrf}})
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}

	resp := post("pausar")
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/dashboard" {
		t.Fatalf("pausar status=%d location=%s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if status, ok := jobRowStatus(t, db, 1); !ok || status != "paused_by_user" {
		t.Fatalf("status=%q ok=%v", status, ok)
	}
	if jobsChangedCount != 1 {
		t.Fatalf("jobsChangedCount=%d after pausar, want 1", jobsChangedCount)
	}
	if len(jobCreatedIDs) != 0 {
		t.Fatalf("OnJobCreated invoked on pausar: %v", jobCreatedIDs)
	}

	resp = post("reanudar")
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("reanudar status=%d", resp.StatusCode)
	}
	if status, ok := jobRowStatus(t, db, 1); !ok || status != "active" {
		t.Fatalf("status=%q ok=%v", status, ok)
	}
	if jobsChangedCount != 2 {
		t.Fatalf("jobsChangedCount=%d after reanudar, want 2", jobsChangedCount)
	}
	if len(jobCreatedIDs) != 1 || jobCreatedIDs[0] != "1" {
		t.Fatalf("OnJobCreated=%v after reanudar, want [\"1\"]", jobCreatedIDs)
	}

	resp = post("detener")
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("detener status=%d", resp.StatusCode)
	}
	if _, ok := jobRowStatus(t, db, 1); ok {
		t.Fatal("job row still present after detener")
	}
	if jobsChangedCount != 3 {
		t.Fatalf("jobsChangedCount=%d after detener, want 3", jobsChangedCount)
	}
	if len(jobCreatedIDs) != 1 {
		t.Fatalf("OnJobCreated invoked on detener: %v", jobCreatedIDs)
	}
}

func TestJobActionRejectsInvalidActionAndUnknownJob(t *testing.T) {
	db := seedJobActionDB(t)
	var jobsChangedCount int
	s := &Server{
		User: "admin", Password: "secret", SessionSecret: []byte("0123456789abcdef0123456789abcdef"), DB: db,
		OnJobsChanged: func() { jobsChangedCount++ },
	}
	httpServer := httptest.NewServer(s)
	defer httpServer.Close()
	client := authenticatedClient(t, httpServer, "admin", "secret")
	dashboardResp, err := client.Get(httpServer.URL + "/dashboard")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(dashboardResp.Body)
	dashboardResp.Body.Close()
	csrf := string(csrfPattern.FindSubmatch(body)[1])

	resp, err := client.PostForm(httpServer.URL+"/jobs/accion", url.Values{"id": {"1"}, "accion": {"borrar"}, "_csrf": {csrf}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid action status=%d", resp.StatusCode)
	}
	if status, ok := jobRowStatus(t, db, 1); !ok || status != "active" {
		t.Fatalf("status=%q ok=%v", status, ok)
	}

	resp, err = client.PostForm(httpServer.URL+"/jobs/accion", url.Values{"id": {"999"}, "accion": {"pausar"}, "_csrf": {csrf}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown job status=%d", resp.StatusCode)
	}
	if jobsChangedCount != 0 {
		t.Fatalf("jobsChangedCount=%d, want 0 (no successful mutation occurred)", jobsChangedCount)
	}
}

func TestDashboardSSRRendersPausarReanudarEliminarButtonsPerJobState(t *testing.T) {
	snapshot := Snapshot{
		BotGuilds: []Guild{},
		Health:    Health{PausedAccounts: PausedHealth{Breakdown: []Breakdown{}}, Jobs: JobsHealth{}},
		Accounts: []Account{{DiscordUserID: "user-a", DisplayName: "Ana", Jobs: []Job{
			{JobID: 1, Label: "Activa", ManuallyPaused: false, Filters: Filters{}},
			{JobID: 2, Label: "Pausada", ManuallyPaused: true, Filters: Filters{}},
		}}},
	}
	var rendered bytes.Buffer
	if err := dashboardTemplate.Execute(&rendered, dashboardView{Snapshot: snapshot, CSRF: "csrf-test-token"}); err != nil {
		t.Fatal(err)
	}
	html := rendered.String()

	activePanelEnd := strings.Index(html, `data-job-id="2"`)
	if activePanelEnd == -1 {
		t.Fatalf("job 2 panel missing: %s", html)
	}
	activePanel := html[:activePanelEnd]
	pausedPanel := html[activePanelEnd:]

	if !strings.Contains(activePanel, `value="pausar"`) || strings.Contains(activePanel, `value="reanudar"`) {
		t.Fatalf("active job panel must show Pausar only: %s", activePanel)
	}
	if !strings.Contains(pausedPanel, `value="reanudar"`) || strings.Contains(pausedPanel, `value="pausar"`) {
		t.Fatalf("manually-paused job panel must show Reanudar only: %s", pausedPanel)
	}
	if strings.Count(html, `value="detener"`) != 2 {
		t.Fatalf("expected a Eliminar button per job panel: %s", html)
	}
	if strings.Count(html, `action="/jobs/accion"`) != 2 {
		t.Fatalf("expected one /jobs/accion form per job panel: %s", html)
	}
	// The logout form (already present pre-existing) also carries a CSRF
	// input, so the total across the page is 3 (logout + one per job panel)
	// -- checked precisely per-panel below via activePanel/pausedPanel.
	if strings.Count(html, `value="csrf-test-token"`) != 3 {
		t.Fatalf("expected the real session CSRF in each job-actions form: %s", html)
	}
	if !strings.Contains(activePanel, `action="/jobs/accion"`) || !strings.Contains(activePanel, `value="csrf-test-token"`) {
		t.Fatalf("active job panel missing CSRF-protected /jobs/accion form: %s", activePanel)
	}
	if !strings.Contains(pausedPanel, `action="/jobs/accion"`) || !strings.Contains(pausedPanel, `value="csrf-test-token"`) {
		t.Fatalf("paused job panel missing CSRF-protected /jobs/accion form: %s", pausedPanel)
	}
}

func TestDashboardEndToEndJobActionButtonsChangeStatusViaHTTP(t *testing.T) {
	db := seedJobActionDB(t)
	source := SnapshotSource{DB: db}
	s := &Server{User: "admin", Password: "secret", SessionSecret: []byte("0123456789abcdef0123456789abcdef"), DB: db, Snapshot: source.Build}
	httpServer := httptest.NewServer(s)
	defer httpServer.Close()
	client := authenticatedClient(t, httpServer, "admin", "secret")

	dashboardResp, err := client.Get(httpServer.URL + "/dashboard")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(dashboardResp.Body)
	dashboardResp.Body.Close()
	html := string(body)
	csrf := string(csrfPattern.FindSubmatch(body)[1])
	jobIDMatch := regexp.MustCompile(`data-job-id="(\d+)"`).FindStringSubmatch(html)
	if len(jobIDMatch) != 2 {
		t.Fatalf("job id missing from dashboard HTML: %s", html)
	}
	jobID := jobIDMatch[1]

	pause, err := client.PostForm(httpServer.URL+"/jobs/accion", url.Values{"id": {jobID}, "accion": {"pausar"}, "_csrf": {csrf}})
	if err != nil {
		t.Fatal(err)
	}
	pauseBody, _ := io.ReadAll(pause.Body)
	pause.Body.Close()
	if pause.Request.URL.Path != "/dashboard" {
		t.Fatalf("pausar did not redirect to /dashboard: path=%s body=%s", pause.Request.URL.Path, pauseBody)
	}

	api, err := client.Get(httpServer.URL + "/api/dashboard")
	if err != nil {
		t.Fatal(err)
	}
	apiBody, _ := io.ReadAll(api.Body)
	api.Body.Close()
	apiJSON := string(apiBody)
	if !strings.Contains(apiJSON, `"manuallyPaused":true`) || !strings.Contains(apiJSON, `"status":{"code":"paused_by_user"`) {
		t.Fatalf("api/dashboard did not reflect pausar: %s", apiJSON)
	}

	resume, err := client.PostForm(httpServer.URL+"/jobs/accion", url.Values{"id": {jobID}, "accion": {"reanudar"}, "_csrf": {csrf}})
	if err != nil {
		t.Fatal(err)
	}
	resume.Body.Close()

	api, err = client.Get(httpServer.URL + "/api/dashboard")
	if err != nil {
		t.Fatal(err)
	}
	apiBody, _ = io.ReadAll(api.Body)
	api.Body.Close()
	apiJSON = string(apiBody)
	if !strings.Contains(apiJSON, `"manuallyPaused":false`) {
		t.Fatalf("api/dashboard did not reflect reanudar: %s", apiJSON)
	}
}

func TestDashboardRefreshFormatsAndPatchesEveryTimestampInPlace(t *testing.T) {
	var rendered bytes.Buffer
	if err := dashboardTemplate.Execute(&rendered, dashboardView{Snapshot: Snapshot{BotGuilds: []Guild{}, Accounts: []Account{}, Health: Health{PausedAccounts: PausedHealth{Breakdown: []Breakdown{}}}}}); err != nil {
		t.Fatal(err)
	}
	script := rendered.String()
	for _, want := range []string{"es-AR", "America/Argentina/Buenos_Aires", "formatTimestamp", "generated-at", "health-last-success", "account-pause", "account-last-poll", "job-last-poll", "history-recorded-at", "textContent"} {
		if !strings.Contains(script, want) {
			t.Errorf("refresh script missing %q", want)
		}
	}
	if strings.Contains(script, "dashboard-data').replace") || strings.Contains(script, `dashboard-data").replace`) {
		t.Fatal("refresh replaces dashboard root")
	}
}
