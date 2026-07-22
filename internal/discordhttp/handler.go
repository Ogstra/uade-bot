package discordhttp

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

const maxBody = 1 << 20

// InteractionResponse is the Discord interaction callback payload. Data is
// intentionally opaque so command handlers never need to serialize secrets.
type InteractionResponse struct {
	Type int `json:"type"`
	Data any `json:"data,omitempty"`
}

type Dispatcher interface {
	Dispatch(context.Context, []byte) (InteractionResponse, error)
}

type DispatchFunc func(context.Context, []byte) (InteractionResponse, error)

func (f DispatchFunc) Dispatch(ctx context.Context, body []byte) (InteractionResponse, error) {
	return f(ctx, body)
}

// Handler verifies Discord's signature before parsing or dispatching. A small
// replay cache and timestamp window prevent a captured valid request from being
// accepted twice. Dispatch has a hard deadline below Discord's three seconds.
type Handler struct {
	PublicKey ed25519.PublicKey
	Dispatch  Dispatcher
	Now       func() time.Time
	MaxAge    time.Duration
	mu        sync.Mutex
	seen      map[[32]byte]time.Time
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil || len(body) > maxBody {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	sig, sigErr := hex.DecodeString(r.Header.Get("X-Signature-Ed25519"))
	tsText := r.Header.Get("X-Signature-Timestamp")
	if sigErr != nil || len(sig) != ed25519.SignatureSize || len(h.PublicKey) != ed25519.PublicKeySize ||
		!ed25519.Verify(h.PublicKey, append([]byte(tsText), body...), sig) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	now := time.Now()
	if h.Now != nil {
		now = h.Now()
	}
	maxAge := h.MaxAge
	if maxAge == 0 {
		maxAge = 5 * time.Minute
	}
	ts, parseErr := time.Parse(time.RFC3339, tsText)
	if parseErr != nil {
		if unix, err := parseUnix(tsText); err == nil {
			ts = time.Unix(unix, 0)
		} else {
			http.Error(w, "invalid timestamp", http.StatusUnauthorized)
			return
		}
	}
	if age := now.Sub(ts); age > maxAge || age < -maxAge {
		http.Error(w, "stale interaction", http.StatusUnauthorized)
		return
	}
	key := sha256.Sum256(append(append([]byte(nil), sig...), body...))
	if h.replayed(key, now, maxAge) {
		http.Error(w, "replayed interaction", http.StatusUnauthorized)
		return
	}

	var envelope struct {
		Type int `json:"type"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Type == 0 {
		http.Error(w, "invalid interaction", http.StatusBadRequest)
		return
	}
	response := InteractionResponse{Type: 1}
	if envelope.Type != 1 {
		if h.Dispatch == nil {
			http.Error(w, "dispatcher unavailable", http.StatusServiceUnavailable)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2500*time.Millisecond)
		defer cancel()
		response, err = h.Dispatch.Dispatch(ctx, body)
		if err != nil {
			http.Error(w, "interaction failed", http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}

func (h *Handler) replayed(key [32]byte, now time.Time, ttl time.Duration) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.seen == nil {
		h.seen = make(map[[32]byte]time.Time)
	}
	for value, at := range h.seen {
		if now.Sub(at) > ttl {
			delete(h.seen, value)
		}
	}
	if _, ok := h.seen[key]; ok {
		return true
	}
	h.seen[key] = now
	return false
}

func parseUnix(value string) (int64, error) {
	var unix int64
	_, err := fmt.Sscan(value, &unix)
	return unix, err
}
