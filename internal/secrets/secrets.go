// Package secrets encrypts values stored in the database (API keys) with
// AES-256-GCM. The key comes from OFFICE_SECRET_KEY (base64, 32 bytes) or a
// 0600 key file created on first use in the office home.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnvKey overrides the key file (useful for containers and secret managers).
const EnvKey = "OFFICE_SECRET_KEY"

// Box seals and opens strings.
type Box struct{ aead cipher.AEAD }

// Load reads the key from EnvKey, else from keyPath (creating it if missing).
func Load(keyPath string) (*Box, error) {
	key, err := loadKey(keyPath)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

func loadKey(path string) ([]byte, error) {
	if v := strings.TrimSpace(os.Getenv(EnvKey)); v != "" {
		k, err := base64.StdEncoding.DecodeString(v)
		if err != nil || len(k) != 32 {
			return nil, fmt.Errorf("secrets: %s must be base64 of 32 bytes", EnvKey)
		}
		return k, nil
	}
	raw, err := os.ReadFile(path)
	if err == nil {
		k, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
		if err != nil || len(k) != 32 {
			return nil, fmt.Errorf("secrets: key file %s is corrupt", path)
		}
		return k, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	// O_EXCL: never overwrite a key another process just created.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return loadKey(path)
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.WriteString(base64.StdEncoding.EncodeToString(k) + "\n"); err != nil {
		return nil, err
	}
	return k, nil
}

// Seal encrypts plaintext as "v1:<base64(nonce||ciphertext)>". Empty stays empty.
func (b *Box) Seal(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := b.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return "v1:" + base64.StdEncoding.EncodeToString(out), nil
}

// Open decrypts a value produced by Seal.
func (b *Box) Open(sealed string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	rest, ok := strings.CutPrefix(sealed, "v1:")
	if !ok {
		return "", errors.New("secrets: unknown format")
	}
	raw, err := base64.StdEncoding.DecodeString(rest)
	if err != nil || len(raw) < b.aead.NonceSize() {
		return "", errors.New("secrets: malformed value")
	}
	n := b.aead.NonceSize()
	pt, err := b.aead.Open(nil, raw[:n], raw[n:], nil)
	if err != nil {
		return "", errors.New("secrets: cannot decrypt (wrong key?)")
	}
	return string(pt), nil
}

// Hint returns a display-safe tail of a secret, e.g. "…WXYZ".
func Hint(secret string) string {
	if len(secret) < 12 {
		return "…"
	}
	return "…" + secret[len(secret)-4:]
}
