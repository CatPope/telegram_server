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

const (
	Argon2Memory      uint32 = 64 * 1024
	Argon2Iterations  uint32 = 3
	Argon2Parallelism uint8  = 1
	Argon2KeyLen      uint32 = 32
	Argon2SaltLen     uint32 = 16
)

var (
	ErrInvalidHashFormat = errors.New("auth: argon2id encoded hash malformed")
	ErrUnsupportedParams = errors.New("auth: argon2id parameters do not match pinned constants")
)

func HashAPIKey(cleartext string) (string, error) {
	salt := make([]byte, Argon2SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: read salt: %w", err)
	}
	return encodeArgon2id(cleartext, salt), nil
}

func encodeArgon2id(cleartext string, salt []byte) string {
	key := argon2.IDKey(
		[]byte(cleartext),
		salt,
		Argon2Iterations,
		Argon2Memory,
		Argon2Parallelism,
		Argon2KeyLen,
	)
	return fmt.Sprintf(
		"$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		Argon2Memory,
		Argon2Iterations,
		Argon2Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	)
}

func VerifyAPIKey(cleartext, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false, ErrInvalidHashFormat
	}
	if parts[2] != "v=19" {
		return false, ErrInvalidHashFormat
	}
	var mem, iter uint32
	var par uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &iter, &par); err != nil {
		return false, ErrInvalidHashFormat
	}
	if mem != Argon2Memory || iter != Argon2Iterations || par != Argon2Parallelism {
		return false, ErrUnsupportedParams
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, ErrInvalidHashFormat
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, ErrInvalidHashFormat
	}
	got := argon2.IDKey(
		[]byte(cleartext),
		salt,
		iter,
		mem,
		par,
		uint32(len(want)),
	)
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
