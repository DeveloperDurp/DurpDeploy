// Package secret implements AES-256-GCM encryption for the `value` column
// of the variables / release_variables tables (P1-3, secret encryption at
// rest). The key never touches the database; it comes from a file or an
// environment variable, and the server refuses to boot without one.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// KeyEnvVar and KeyFilePath are the two supported sources for the 32-byte
// AES-256 key, checked in that order (file first, then env).
const (
	KeyEnvVar   = "DURPDEPLOY_SECRET_KEY"
	KeyFilePath = "/etc/durpdeploy/key"
)

// Box encrypts/decrypts variable values with AES-256-GCM. The zero value is
// not usable; construct with NewBox.
type Box struct {
	aead cipher.AEAD
}

// LoadKey reads the key from KeyFilePath, falling back to KeyEnvVar
// (base64-encoded, 32 bytes after decoding). Returns an error — callers
// must refuse to start — if neither source is configured.
func LoadKey() ([]byte, error) {
	if data, err := os.ReadFile(KeyFilePath); err == nil {
		key, err := decodeKey(string(data))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", KeyFilePath, err)
		}
		return key, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read key file %s: %w", KeyFilePath, err)
	}

	if v := os.Getenv(KeyEnvVar); v != "" {
		key, err := decodeKey(v)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", KeyEnvVar, err)
		}
		return key, nil
	}

	return nil, fmt.Errorf(
		"no secret key configured: set %s (32 random bytes, base64) or "+
			"create %s — refusing to start with variables stored in plaintext",
		KeyEnvVar, KeyFilePath,
	)
}

func decodeKey(raw string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("decode base64 key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf(
			"key must be 32 bytes after base64 decoding, got %d",
			len(key),
		)
	}
	return key, nil
}

// NewBox builds a Box from a raw 32-byte AES-256 key.
func NewBox(key []byte) (*Box, error) {
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

// Encrypt returns base64(nonce || ciphertext || tag). Empty plaintext maps
// to empty ciphertext so NULL/empty variable values stay NULL/empty.
func (b *Box) Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := b.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt reverses Encrypt. The returned error never contains the
// plaintext or the raw ciphertext — safe to log or surface to a caller.
func (b *Box) Decrypt(ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", errors.New("decrypt: invalid ciphertext encoding")
	}
	nonceSize := b.aead.NonceSize()
	if len(raw) < nonceSize {
		return "", errors.New("decrypt: ciphertext too short")
	}
	nonce, data := raw[:nonceSize], raw[nonceSize:]
	plaintext, err := b.aead.Open(nil, nonce, data, nil)
	if err != nil {
		return "", errors.New("decrypt: authentication failed (wrong key?)")
	}
	return string(plaintext), nil
}

// EncryptNullString / DecryptNullString adapt Encrypt/Decrypt to the
// sql.NullString shape used by the variables/release_variables `value`
// column.
func (b *Box) EncryptNullString(v sql.NullString) (sql.NullString, error) {
	if !v.Valid || v.String == "" {
		return v, nil
	}
	enc, err := b.Encrypt(v.String)
	if err != nil {
		return sql.NullString{}, err
	}
	return sql.NullString{String: enc, Valid: true}, nil
}

func (b *Box) DecryptNullString(v sql.NullString) (sql.NullString, error) {
	if !v.Valid || v.String == "" {
		return v, nil
	}
	dec, err := b.Decrypt(v.String)
	if err != nil {
		return sql.NullString{}, err
	}
	return sql.NullString{String: dec, Valid: true}, nil
}
