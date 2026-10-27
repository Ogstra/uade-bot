package entra

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/crypto/scrypt"
)

// Store persists exactly one secret: the current refresh token.
//
// The key is derived with scrypt from Secret (an environment variable) and a
// salt that is fresh on every write, so the same secret never encrypts two
// files under the same key. The key itself is never written anywhere: losing
// the environment variable makes the file unreadable by design, and copying
// the file alone gives an attacker nothing to decrypt it with.
type Store struct {
	Path   string
	Secret []byte
	// Subject is whose token this file holds (a Discord user id). It is bound
	// into the ciphertext, so copying one user's file over another's does not
	// decrypt: each file only opens as the account it was written for.
	Subject string
	Random  io.Reader
}

// ErrNoToken means there is no stored session: first run, or a session that
// was deliberately cleared after dying.
var ErrNoToken = errors.New("entra: no stored refresh token")

// ErrStoreUnreadable covers a tampered file, a truncated file and a wrong
// secret alike. They are indistinguishable on purpose -- telling them apart
// would turn the error message into an oracle -- and all three fail closed:
// the caller treats the session as absent and asks for a new sign-in, never
// running on a token it could not authenticate.
var ErrStoreUnreadable = errors.New("entra: stored refresh token is unreadable")

const (
	fileVersion = 1
	// scrypt N=2^15, r=8, p=1: the interactive-login recommendation, ~100ms
	// per derivation. Paid once per refresh (about hourly), never per request.
	scryptN = 1 << 15
	scryptR = 8
	scryptP = 1
	keyLen  = 32
	saltLen = 16
)

// additionalData binds the ciphertext to its purpose and its owner, so a blob
// encrypted for anything else -- or for another user -- under the same secret
// cannot be swapped in as this user's refresh token.
func (s *Store) additionalData() []byte {
	return []byte("uade-bot/entra/refresh-token/v1/" + s.Subject)
}

type envelope struct {
	Version    int    `json:"v"`
	Salt       []byte `json:"salt"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ct"`
}

func (s *Store) random() io.Reader {
	if s.Random != nil {
		return s.Random
	}
	return rand.Reader
}

func (s *Store) aead(salt []byte) (cipher.AEAD, error) {
	if len(s.Secret) == 0 {
		return nil, errors.New("entra: store secret is empty")
	}
	key, err := scrypt.Key(s.Secret, salt, scryptN, scryptR, scryptP, keyLen)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Save writes atomically: a crash mid-write leaves either the previous file or
// the new one, never a half-written token. That matters more than usual here
// because Microsoft rotates refresh tokens -- losing the new one after the old
// one was already consumed would kill the session outright.
func (s *Store) Save(refreshToken string) error {
	if refreshToken == "" {
		return errors.New("entra: refusing to store an empty refresh token")
	}
	salt := make([]byte, saltLen)
	if _, err := io.ReadFull(s.random(), salt); err != nil {
		return err
	}
	aead, err := s.aead(salt)
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = io.ReadFull(s.random(), nonce); err != nil {
		return err
	}
	data, err := json.Marshal(envelope{
		Version:    fileVersion,
		Salt:       salt,
		Nonce:      nonce,
		Ciphertext: aead.Seal(nil, nonce, []byte(refreshToken), s.additionalData()),
	})
	if err != nil {
		return err
	}

	dir := filepath.Dir(s.Path)
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".entra-token-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once renamed
	// CreateTemp already opens 0600, but say it explicitly: it is the contract.
	if err = tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, s.Path)
}

func (s *Store) Load() (string, error) {
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNoToken
	}
	if err != nil {
		return "", err
	}
	var env envelope
	if json.Unmarshal(data, &env) != nil || env.Version != fileVersion || len(env.Salt) != saltLen {
		return "", ErrStoreUnreadable
	}
	aead, err := s.aead(env.Salt)
	if err != nil {
		return "", err
	}
	if len(env.Nonce) != aead.NonceSize() {
		return "", ErrStoreUnreadable
	}
	plain, err := aead.Open(nil, env.Nonce, env.Ciphertext, s.additionalData())
	if err != nil || len(plain) == 0 {
		return "", ErrStoreUnreadable
	}
	return string(plain), nil
}

// Clear removes the stored token. Called when the session dies, so a dead
// refresh token is not retried on every restart.
func (s *Store) Clear() error {
	err := os.Remove(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
