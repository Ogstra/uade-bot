package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
)

const hkdfInfo = "uade-bot-credentials-v1"

type Credentials struct {
	UADEUsername string `json:"uadeUsername"`
	UADEPassword string `json:"uadePassword"`
	UADEStartURL string `json:"uadeStartUrl"`
}
type Ciphertext struct {
	Ciphertext string `json:"ciphertext"`
	IV         string `json:"iv"`
	AuthTag    string `json:"authTag"`
}

func deriveUserKey(masterHex, userID string) ([]byte, error) {
	master, err := hex.DecodeString(masterHex)
	if err != nil || len(master) != 32 {
		return nil, fmt.Errorf("master key must be 32-byte hex")
	}
	key, err := hkdf.Key(sha256.New, master, []byte(userID), hkdfInfo, 32)
	if err != nil {
		return nil, err
	}
	return key, nil
}

func Encrypt(masterHex, userID string, values Credentials) (Ciphertext, error) {
	key, err := deriveUserKey(masterHex, userID)
	if err != nil {
		return Ciphertext{}, err
	}
	b, err := json.Marshal(values)
	if err != nil {
		return Ciphertext{}, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return Ciphertext{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return Ciphertext{}, err
	}
	iv := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, iv); err != nil {
		return Ciphertext{}, err
	}
	sealed := gcm.Seal(nil, iv, b, nil)
	tagLen := gcm.Overhead()
	ct, tag := sealed[:len(sealed)-tagLen], sealed[len(sealed)-tagLen:]
	return Ciphertext{base64.StdEncoding.EncodeToString(ct), base64.StdEncoding.EncodeToString(iv), base64.StdEncoding.EncodeToString(tag)}, nil
}

func Decrypt(masterHex, userID string, c Ciphertext) (Credentials, error) {
	key, err := deriveUserKey(masterHex, userID)
	if err != nil {
		return Credentials{}, err
	}
	ct, err := base64.StdEncoding.DecodeString(c.Ciphertext)
	if err != nil {
		return Credentials{}, err
	}
	iv, err := base64.StdEncoding.DecodeString(c.IV)
	if err != nil {
		return Credentials{}, err
	}
	tag, err := base64.StdEncoding.DecodeString(c.AuthTag)
	if err != nil {
		return Credentials{}, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return Credentials{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return Credentials{}, err
	}
	plain, err := gcm.Open(nil, iv, append(ct, tag...), nil)
	if err != nil {
		return Credentials{}, fmt.Errorf("decrypt credentials: %w", err)
	}
	var out Credentials
	if err := json.Unmarshal(plain, &out); err != nil {
		return Credentials{}, err
	}
	return out, nil
}
