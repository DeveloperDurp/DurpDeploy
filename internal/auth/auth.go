package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argon2Time    = 2
	argon2Memory  = 65536
	argon2Threads = 2
	argon2SaltLen = 16
	argon2KeyLen  = 32
)

// HashPassword hashes the given plaintext password using argon2id with
// the project's default parameters. Returns a PHC-formatted string:
//
//	$argon2id$v=19$m=65536,t=2,p=2$<salt-b64>$<hash-b64>
//
// where <salt-b64> and <hash-b64> are base64.RawStdEncoding (no padding).
func HashPassword(password string) (string, error) {
	salt := make([]byte, argon2SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}

	hash := argon2.IDKey([]byte(password), salt, argon2Time, argon2Memory, argon2Threads, argon2KeyLen)

	b64Salt := base64.RawStdEncoding.EncodeToString(salt)
	b64Hash := base64.RawStdEncoding.EncodeToString(hash)

	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argon2Memory, argon2Time, argon2Threads, b64Salt, b64Hash), nil
}

// VerifyPassword parses the PHC string and verifies the password against
// the hash. Returns true iff the password matches.
func VerifyPassword(hash, password string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 {
		return false
	}
	if parts[1] != "argon2id" {
		return false
	}
	if parts[2] != "v=19" {
		return false
	}

	var mem, time uint32
	var threads uint8
	params := strings.Split(parts[3], ",")
	if len(params) != 3 {
		return false
	}
	for _, p := range params {
		kv := strings.SplitN(p, "=", 2)
		if len(kv) != 2 {
			return false
		}
		switch kv[0] {
		case "m":
			v, err := strconv.ParseUint(kv[1], 10, 32)
			if err != nil {
				return false
			}
			mem = uint32(v)
		case "t":
			v, err := strconv.ParseUint(kv[1], 10, 32)
			if err != nil {
				return false
			}
			time = uint32(v)
		case "p":
			v, err := strconv.ParseUint(kv[1], 10, 8)
			if err != nil {
				return false
			}
			threads = uint8(v)
		default:
			return false
		}
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	expectedHash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	keyLen := uint32(len(expectedHash))

	computedHash := argon2.IDKey([]byte(password), salt, time, mem, threads, keyLen)

	return subtle.ConstantTimeCompare(computedHash, expectedHash) == 1
}

// NewSessionToken generates a fresh session token and CSRF token, both
// cryptographically random. The session token is 32 bytes hex-encoded
// (64 hex chars). The CSRF token is 16 bytes hex-encoded (32 hex chars).
// Both come from crypto/rand.
func NewSessionToken() (token, csrf string, err error) {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", "", fmt.Errorf("generate session token: %w", err)
	}

	csrfBytes := make([]byte, 16)
	if _, err := rand.Read(csrfBytes); err != nil {
		return "", "", fmt.Errorf("generate csrf token: %w", err)
	}

	return hex.EncodeToString(tokenBytes), hex.EncodeToString(csrfBytes), nil
}
