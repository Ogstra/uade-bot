package discordgateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Ogstra/uade-bot/internal/discordhttp"
)

func TestRespondPostsNoBotAuthCallback(t *testing.T) {
	var gotMethod, gotPath, gotContentType, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	err := Respond(context.Background(), server.Client(), server.URL, "123", "tok", discordhttp.InteractionResponse{Type: 4})
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %s", gotMethod)
	}
	if gotPath != "/interactions/123/tok/callback" {
		t.Fatalf("path = %s", gotPath)
	}
	if gotContentType != "application/json" {
		t.Fatalf("content-type = %s", gotContentType)
	}
	if gotAuth != "" {
		t.Fatalf("authorization header present: %q", gotAuth)
	}
}

func TestRespondReturnsErrorOnFailureStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	if err := Respond(context.Background(), server.Client(), server.URL, "123", "tok", discordhttp.InteractionResponse{Type: 4}); err == nil {
		t.Fatal("expected error on >=300 status")
	}
}
