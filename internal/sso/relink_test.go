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

func TestSSOURLHelpersFailClosed(t *testing.T) {
	if MicrosoftEmail("jperez") != "jperez@uade.edu.ar" || MicrosoftEmail("jperez@example.com") != "jperez@example.com" {
		t.Fatal("Microsoft email normalization mismatch")
	}
	if !IsMicrosoftLogin("https://login.microsoftonline.com/tenant") || IsMicrosoftLogin("https://login.microsoftonline.com.evil.example/") {
		t.Fatal("Microsoft host validation mismatch")
	}
	if !ValidStartURL("https://inscripcionespia.uade.edu.ar/x?param=abc") {
		t.Fatal("valid enrollment URL rejected")
	}
	for _, candidate := range []string{
		"http://inscripcionespia.uade.edu.ar/x?param=abc",
		"https://inscripcionespia.uade.edu.ar.evil.example/x?param=abc",
		"https://inscripcionespia.uade.edu.ar/x?other=abc",
	} {
		if ValidStartURL(candidate) {
			t.Fatalf("accepted %q", candidate)
		}
	}
}

func TestRelinkRejectsUnverifiedFinalURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer srv.Close()
	_, err := Relink(context.Background(), http.DefaultClient, srv.URL, "u", "p")
	if err != ErrInvalidStartURL {
		t.Fatalf("err=%v", err)
	}
}

func TestRelinkLoadsFallbackOnlyForMFA(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer srv.Close()
	called := false
	result, err := RelinkWithFallback(context.Background(), http.DefaultClient, srv.URL, "u", "p", func(context.Context, string, string) (Result, error) {
		called = true
		return Result{Manual: true}, nil
	})
	if err != nil || !called || !result.Manual {
		t.Fatalf("result=%+v called=%v err=%v", result, called, err)
	}
}
