package dashboard

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestServerLoginSSRAndJSONRefresh(t *testing.T) {
	now := time.Unix(1_750_000_000, 0)
	s := &Server{User: "admin", Password: "secret", SessionSecret: []byte("0123456789abcdef0123456789abcdef"), Now: func() time.Time { return now }}
	httpServer := httptest.NewServer(s)
	defer httpServer.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	login, err := client.Get(httpServer.URL + "/login")
	if err != nil || login.StatusCode != http.StatusOK {
		t.Fatalf("login: status=%v err=%v", login.StatusCode, err)
	}
	body, _ := io.ReadAll(login.Body)
	login.Body.Close()
	if !strings.Contains(string(body), "Dashboard de UADE Bot") || login.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("invalid login response")
	}

	response, err := client.PostForm(httpServer.URL+"/login", url.Values{"username": {"admin"}, "password": {"secret"}})
	if err != nil || response.Request.URL.Path != "/dashboard" {
		t.Fatalf("auth flow: path=%v err=%v", response.Request.URL.Path, err)
	}
	body, _ = io.ReadAll(response.Body)
	response.Body.Close()
	if !strings.Contains(string(body), "api/dashboard") || !strings.Contains(string(body), "setInterval") {
		t.Fatal("SSR must include refresh client")
	}

	api, _ := client.Get(httpServer.URL + "/api/dashboard")
	apiBody, _ := io.ReadAll(api.Body)
	api.Body.Close()
	if api.StatusCode != http.StatusOK || !strings.Contains(string(apiBody), `"status":"ok"`) {
		t.Fatalf("api status=%d body=%s", api.StatusCode, apiBody)
	}
}

func TestServerRejectsUnauthorizedAndTamperedSession(t *testing.T) {
	s := &Server{User: "a", Password: "b", SessionSecret: []byte("0123456789abcdef0123456789abcdef")}
	request := httptest.NewRequest(http.MethodGet, "/api/dashboard", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookie, Value: "attacker.invalid"})
	response := httptest.NewRecorder()
	s.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || strings.Contains(response.Body.String(), "attacker") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestHealthIsPublicAndHasSecurityHeaders(t *testing.T) {
	s := &Server{}
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()
	s.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("status=%d headers=%v", response.Code, response.Header())
	}
}
