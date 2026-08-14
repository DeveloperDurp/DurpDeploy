package mfa

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	recoveryCodeCount       = 10
	recoveryCodeBytes       = 16
	recoveryCodeMaxAttempts = 100
)

var (
	ErrInvalidRecoveryCode = errors.New("invalid recovery code")
	ErrRecoveryRandom      = errors.New("generate recovery code")
)

// GenerateRecoveryCodes returns ten unique, human-readable 128-bit codes.
// Callers must display each code once and persist only its hash.
func GenerateRecoveryCodes(random io.Reader) ([]string, error) {
	if random == nil {
		random = rand.Reader
	}
	codes := make([]string, 0, recoveryCodeCount)
	seen := make(map[string]struct{}, recoveryCodeCount)
	for attempts := 0; len(codes) < recoveryCodeCount; attempts++ {
		if attempts == recoveryCodeMaxAttempts {
			return nil, ErrRecoveryRandom
		}
		raw := make([]byte, recoveryCodeBytes)
		if _, err := io.ReadFull(random, raw); err != nil {
			return nil, fmt.Errorf(
				"read recovery code randomness: %w",
				ErrRecoveryRandom,
			)
		}
		code := formatRecoveryCode(raw)
		if _, exists := seen[code]; exists {
			continue
		}
		seen[code] = struct{}{}
		codes = append(codes, code)
	}
	return codes, nil
}

// NormalizeRecoveryCode returns the canonical uppercase, ungrouped form.
func NormalizeRecoveryCode(code string) (string, error) {
	normalized := strings.ToUpper(
		strings.ReplaceAll(strings.TrimSpace(code), "-", ""),
	)
	if len(normalized) != recoveryCodeBytes*2 {
		return "", ErrInvalidRecoveryCode
	}
	if _, err := hex.DecodeString(normalized); err != nil {
		return "", ErrInvalidRecoveryCode
	}
	return normalized, nil
}

// HashRecoveryCode returns the SHA-256 hash suitable for persistence.
func HashRecoveryCode(code string) ([sha256.Size]byte, error) {
	normalized, err := NormalizeRecoveryCode(code)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256([]byte(normalized)), nil
}

// RecoveryCodeMatches compares a submitted code with its stored hash in
// constant time.
func RecoveryCodeMatches(
	stored [sha256.Size]byte,
	submitted string,
) (bool, error) {
	hash, err := HashRecoveryCode(submitted)
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(stored[:], hash[:]) == 1, nil
}

func formatRecoveryCode(raw []byte) string {
	encoded := strings.ToUpper(hex.EncodeToString(raw))
	groups := make([]string, 0, len(encoded)/4)
	for index := 0; index < len(encoded); index += 4 {
		groups = append(groups, encoded[index:index+4])
	}
	return strings.Join(groups, "-")
}
