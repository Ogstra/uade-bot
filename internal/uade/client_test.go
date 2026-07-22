package uade

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientUsesCookiesAndBasicAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, _, ok := r.BasicAuth()
		if !ok || user != "uade" || r.Method != http.MethodGet {
			t.Errorf("bad request")
		}
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "ok"})
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html>ok</html>"))
	}))
	defer srv.Close()
	c, err := NewClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, err := c.Fetch(context.Background(), "/search", "uade", "secret")
	if err != nil || body == "" {
		t.Fatalf("body=%q err=%v", body, err)
	}
}

func TestClientRejectsCrossOriginRedirect(t *testing.T) {
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("cross-origin destination must never be requested")
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, &http.Request{}, destination.URL, http.StatusFound)
	}))
	defer source.Close()
	c, _ := NewClient(source.URL)
	if _, err := c.Fetch(context.Background(), "/", "u", "p"); err == nil {
		t.Fatal("expected cross-origin redirect failure")
	}
}

func TestClientEnforcesResponseLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 11)))
	}))
	defer srv.Close()
	c, _ := NewClient(srv.URL)
	c.MaxBodyBytes = 10
	if _, err := c.Fetch(context.Background(), "/", "u", "p"); !errors.Is(err, ErrTransient) {
		t.Fatalf("err=%v", err)
	}
}

func TestClientClassifiesAuthAndTransient(t *testing.T) {
	for code, want := range map[int]error{http.StatusUnauthorized: ErrAuth, http.StatusTooManyRequests: ErrTransient} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }))
		c, _ := NewClient(srv.URL)
		_, err := c.Fetch(context.Background(), "/", "u", "p")
		srv.Close()
		if !errors.Is(err, want) {
			t.Fatalf("status=%d err=%v", code, err)
		}
	}
}
