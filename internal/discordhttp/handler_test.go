package discordhttp

import (
	"crypto/ed25519"
	"encoding/hex"
	"io"
	"net/http/httptest"
	"testing"
)

func TestHandlerVerifiesInteraction(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	body := []byte(`{"type":1}`)
	ts := "1"
	sig := ed25519.Sign(priv, append([]byte(ts), body...))
	r := httptest.NewRequest("POST", "/", bytesReader(body))
	r.Header.Set("X-Signature-Timestamp", ts)
	r.Header.Set("X-Signature-Ed25519", hex.EncodeToString(sig))
	w := httptest.NewRecorder()
	Handler{PublicKey: pub}.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("%d", w.Code)
	}
}

type reader struct{ b []byte }

func (r *reader) Read(p []byte) (int, error) {
	if len(r.b) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.b)
	r.b = r.b[n:]
	return n, nil
}
func bytesReader(b []byte) *reader { return &reader{b: b} }
