package sso

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRelinkMFAFallsBackManual(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(403) }))
	defer srv.Close()
	r, e := Relink(context.Background(), http.DefaultClient, srv.URL, "u", "p")
	if !r.Manual || e != ErrMFARequired {
		t.Fatalf("%+v %v", r, e)
	}
}
