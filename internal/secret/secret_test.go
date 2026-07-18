package secret_test

import (
	"database/sql"
	"encoding/base64"
	"strings"
	"testing"

	"durpdeploy/internal/secret"
)

func base64Encode(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}

func mustBox(t *testing.T, key []byte) *secret.Box {
	t.Helper()
	box, err := secret.NewBox(key)
	if err != nil {
		t.Fatalf("NewBox: %v", err)
	}
	return box
}

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	key := make([]byte, 32)
	box := mustBox(t, key)

	plaintext := "super-secret-api-token"
	ciphertext, err := box.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if ciphertext == plaintext || strings.Contains(ciphertext, plaintext) {
		t.Fatalf("ciphertext must not contain the plaintext, got %q", ciphertext)
	}

	got, err := box.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if got != plaintext {
		t.Fatalf("round trip mismatch: want %q, got %q", plaintext, got)
	}
}

func TestEncrypt_EmptyStringPassesThrough(t *testing.T) {
	box := mustBox(t, make([]byte, 32))

	ciphertext, err := box.Encrypt("")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if ciphertext != "" {
		t.Fatalf("expected empty ciphertext for empty plaintext, got %q", ciphertext)
	}

	plaintext, err := box.Decrypt("")
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if plaintext != "" {
		t.Fatalf("expected empty plaintext for empty ciphertext, got %q", plaintext)
	}
}

func TestDecrypt_WrongKeyFails(t *testing.T) {
	key1 := make([]byte, 32)
	key2 := make([]byte, 32)
	key2[0] = 1 // different key

	box1 := mustBox(t, key1)
	box2 := mustBox(t, key2)

	ciphertext, err := box1.Encrypt("classified")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	if _, err := box2.Decrypt(ciphertext); err == nil {
		t.Fatal("expected decryption with the wrong key to fail")
	}
}

func TestDecrypt_ErrorNeverContainsCiphertextOrPlaintext(t *testing.T) {
	box := mustBox(t, make([]byte, 32))

	_, err := box.Decrypt("not-valid-base64-ciphertext-at-all!!")
	if err == nil {
		t.Fatal("expected an error for garbage ciphertext")
	}
	if strings.Contains(err.Error(), "not-valid-base64-ciphertext-at-all") {
		t.Fatalf("error must not echo the input: %v", err)
	}
}

func TestEncryptNullString_DecryptNullString(t *testing.T) {
	box := mustBox(t, make([]byte, 32))

	// NULL stays NULL.
	null := sql.NullString{}
	enc, err := box.EncryptNullString(null)
	if err != nil {
		t.Fatalf("EncryptNullString: %v", err)
	}
	if enc.Valid {
		t.Fatalf("expected NULL to stay NULL, got %+v", enc)
	}

	// Non-empty value round-trips through the NullString wrappers.
	valid := sql.NullString{String: "hunter2", Valid: true}
	enc, err = box.EncryptNullString(valid)
	if err != nil {
		t.Fatalf("EncryptNullString: %v", err)
	}
	if !enc.Valid || enc.String == valid.String {
		t.Fatalf("expected an encrypted, non-plaintext value, got %+v", enc)
	}

	dec, err := box.DecryptNullString(enc)
	if err != nil {
		t.Fatalf("DecryptNullString: %v", err)
	}
	if dec != valid {
		t.Fatalf("round trip mismatch: want %+v, got %+v", valid, dec)
	}
}

func TestLoadKey_MissingSourceRefuses(t *testing.T) {
	t.Setenv(secret.KeyEnvVar, "")
	if _, err := secret.LoadKey(); err == nil {
		t.Fatal("expected LoadKey to fail when no key source is configured")
	}
}

func TestLoadKey_FromEnv(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	t.Setenv(secret.KeyEnvVar, base64Encode(key))

	got, err := secret.LoadKey()
	if err != nil {
		t.Fatalf("LoadKey: %v", err)
	}
	if len(got) != 32 {
		t.Fatalf("expected 32-byte key, got %d bytes", len(got))
	}
}

func TestLoadKey_RejectsWrongLength(t *testing.T) {
	t.Setenv(secret.KeyEnvVar, base64Encode([]byte("too-short")))
	if _, err := secret.LoadKey(); err == nil {
		t.Fatal("expected LoadKey to reject a key that isn't 32 bytes")
	}
}
