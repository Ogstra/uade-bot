package discordhttp

import (
	"crypto/ed25519"
	"encoding/hex"
	"io"
	"net/http"
)

type Handler struct {
	PublicKey ed25519.PublicKey
	Dispatch  func([]byte)
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad request", 400)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	sig, err1 := hex.DecodeString(r.Header.Get("X-Signature-Ed25519"))
	ts := []byte(r.Header.Get("X-Signature-Timestamp"))
	if err1 != nil || len(sig) != ed25519.SignatureSize || len(h.PublicKey) != ed25519.PublicKeySize || !ed25519.Verify(h.PublicKey, append(ts, body...), sig) {
		http.Error(w, "invalid signature", 401)
		return
	}
	if h.Dispatch != nil {
		go h.Dispatch(body)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"type":1}`))
}
