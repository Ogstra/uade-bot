package entra

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreRoundTrip(t *testing.T) {
	store := newTestStore(t)
	if err := store.Save("RT-roundtrip"); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load()
	if err != nil || got != "RT-roundtrip" {
		t.Fatalf("Load = %q, %v", got, err)
	}
}

func TestStoreNeverWritesThePlaintextOrTheKey(t *testing.T) {
	store := newTestStore(t)
	if err := store.Save("RT-must-not-appear"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(store.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"RT-must-not-appear", string(testSecret)} {
		if strings.Contains(string(raw), leak) {
			t.Fatalf("file contains %q", leak)
		}
	}
}

func TestStoreFileIsOwnerOnly(t *testing.T) {
	store := newTestStore(t)
	if err := store.Save("RT"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(store.Path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("permissions = %o, want 600", perm)
	}
}

// Atomic write: no temp file survives a successful save, so nothing half
// written ever sits next to the real file.
func TestStoreLeavesNoTempFilesBehind(t *testing.T) {
	store := newTestStore(t)
	for i := 0; i < 3; i++ {
		if err := store.Save("RT"); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(filepath.Dir(store.Path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("directory = %v, want only the token file", names)
	}
}

// A fresh salt per write means the same token never encrypts to the same
// bytes, and no two files share a derived key.
func TestStoreUsesAFreshSaltOnEveryWrite(t *testing.T) {
	store := newTestStore(t)
	readSalt := func() []byte {
		raw, _ := os.ReadFile(store.Path)
		var env envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatal(err)
		}
		return env.Salt
	}
	_ = store.Save("RT")
	first := readSalt()
	_ = store.Save("RT")
	if string(first) == string(readSalt()) {
		t.Fatal("salt reused across writes")
	}
}

func TestStoreFailsClosedOnWrongSecret(t *testing.T) {
	store := newTestStore(t)
	if err := store.Save("RT"); err != nil {
		t.Fatal(err)
	}
	wrong := &Store{Path: store.Path, Secret: []byte("another-secret")}

	got, err := wrong.Load()

	if !errors.Is(err, ErrStoreUnreadable) || got != "" {
		t.Fatalf("Load with wrong secret = %q, %v; want ErrStoreUnreadable and no token", got, err)
	}
}

func TestStoreFailsClosedOnTamperedCiphertext(t *testing.T) {
	store := newTestStore(t)
	if err := store.Save("RT"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(store.Path)
	var env envelope
	_ = json.Unmarshal(raw, &env)
	env.Ciphertext[0] ^= 0xff // one flipped bit must be enough
	tampered, _ := json.Marshal(env)
	if err := os.WriteFile(store.Path, tampered, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := store.Load()

	if !errors.Is(err, ErrStoreUnreadable) || got != "" {
		t.Fatalf("Load tampered = %q, %v; want ErrStoreUnreadable", got, err)
	}
}

func TestStoreFailsClosedOnGarbage(t *testing.T) {
	store := newTestStore(t)
	for name, content := range map[string]string{
		"not json":      "hello",
		"empty object":  "{}",
		"wrong version": `{"v":9,"salt":"AAAAAAAAAAAAAAAAAAAAAA==","nonce":"","ct":""}`,
		"truncated":     `{"v":1,"salt":"AAAA`,
	} {
		if err := os.WriteFile(store.Path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if got, err := store.Load(); !errors.Is(err, ErrStoreUnreadable) || got != "" {
			t.Fatalf("%s: Load = %q, %v; want ErrStoreUnreadable", name, got, err)
		}
	}
}

func TestStoreReportsMissingFileDistinctly(t *testing.T) {
	if _, err := newTestStore(t).Load(); !errors.Is(err, ErrNoToken) {
		t.Fatalf("err = %v, want ErrNoToken", err)
	}
}
