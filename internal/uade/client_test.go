package uade

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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
