package discordhttp

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func signedRequest(priv ed25519.PrivateKey, body []byte, timestamp string) *http.Request {
	sig := ed25519.Sign(priv, append([]byte(timestamp), body...))
	r := httptest.NewRequest(http.MethodPost, "/discord/interactions", bytes.NewReader(body))
	r.Header.Set("X-Signature-Timestamp", timestamp)
	r.Header.Set("X-Signature-Ed25519", hex.EncodeToString(sig))
	return r
}

func TestHandlerPingAndSignature(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	now := time.Unix(1_700_000_000, 0)
	w := httptest.NewRecorder()
	(&Handler{PublicKey: pub, Now: func() time.Time { return now }}).ServeHTTP(w, signedRequest(priv, []byte(`{"type":1}`), "1700000000"))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"type":1`) {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

func TestHandlerRejectsInvalidSignatureStaleReplayAndPayload(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	now := time.Unix(1_700_000_000, 0)
	h := &Handler{PublicKey: pub, Now: func() time.Time { return now }}
	tests := []struct {
		name, ts, body string
		mutate         func(*http.Request)
		want           int
	}{
		{"bad signature", "1700000000", `{"type":1}`, func(r *http.Request) { r.Header.Set("X-Signature-Ed25519", "00") }, 401},
		{"stale", "1699990000", `{"type":1}`, nil, 401},
		{"invalid payload", "1700000000", `{}`, nil, 400},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := signedRequest(priv, []byte(tc.body), tc.ts)
			if tc.mutate != nil {
				tc.mutate(r)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
		})
	}
	body := []byte(`{"type":1,"nonce":"unique"}`)
	r := signedRequest(priv, body, "1700000000")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, signedRequest(priv, body, "1700000000"))
	if w.Code != 401 {
		t.Fatalf("replay got %d", w.Code)
	}
}

func TestHandlerDispatchDeadlineAndNoSecretEcho(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	now := time.Unix(1_700_000_000, 0)
	h := &Handler{PublicKey: pub, Now: func() time.Time { return now }, Dispatch: DispatchFunc(func(ctx context.Context, body []byte) (InteractionResponse, error) {
		if strings.Contains(string(body), "super-secret") {
			return message("ok"), nil
		}
		return InteractionResponse{}, ctx.Err()
	})}
	w := httptest.NewRecorder()
	body := []byte(`{"type":2,"data":{"name":"x","password":"super-secret"}}`)
	h.ServeHTTP(w, signedRequest(priv, body, "1700000000"))
	if w.Code != 200 || strings.Contains(w.Body.String(), "super-secret") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

func TestHandlerAlwaysAcknowledgesBeforeDiscordDeadline(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	now := time.Unix(1_700_000_000, 0)
	h := &Handler{PublicKey: pub, Now: func() time.Time { return now }, Dispatch: DispatchFunc(func(ctx context.Context, _ []byte) (InteractionResponse, error) {
		<-ctx.Done()
		return InteractionResponse{}, ctx.Err()
	})}
	started := time.Now()
	w := httptest.NewRecorder()
	body := []byte(`{"type":2,"member":{"user":{"id":"1"}},"data":{"name":"estado"}}`)
	h.ServeHTTP(w, signedRequest(priv, body, "1700000000"))
	if elapsed := time.Since(started); elapsed >= 3*time.Second {
		t.Fatalf("ack took %s", elapsed)
	}
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"type":4`) {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}
