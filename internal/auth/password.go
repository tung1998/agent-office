package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Hasher hashes and verifies passwords.
type Hasher interface {
	Hash(password string) (string, error)
	Verify(encoded, password string) (bool, error)
}

// argon2idHasher encodes as $argon2id$v=19$m=<KiB>,t=<iter>,p=<threads>$<salt>$<hash>.
type argon2idHasher struct {
	memory  uint32
	time    uint32
	threads uint8
	keyLen  uint32
	saltLen int
}

// DefaultHasher uses the OWASP-recommended argon2id baseline (64 MiB, t=3, p=2).
func DefaultHasher() Hasher {
	return argon2idHasher{memory: 64 * 1024, time: 3, threads: 2, keyLen: 32, saltLen: 16}
}

// FastHasherForTests keeps the format but is cheap enough for unit tests.
func FastHasherForTests() Hasher {
	return argon2idHasher{memory: 1024, time: 1, threads: 1, keyLen: 32, saltLen: 16}
}

var errBadHash = errors.New("auth: malformed password hash")

func (h argon2idHasher) Hash(password string) (string, error) {
	salt := make([]byte, h.saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, h.time, h.memory, h.threads, h.keyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, h.memory, h.time, h.threads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// Verify reads the parameters from the encoded hash, so hashes made with other
// parameters (e.g. before a cost increase) still verify.
func (argon2idHasher) Verify(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errBadHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errBadHash
	}
	var memory, iter uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iter, &threads); err != nil {
		return false, errBadHash
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false, errBadHash
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil {
		return false, errBadHash
	}
	got := argon2.IDKey([]byte(password), salt, iter, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
