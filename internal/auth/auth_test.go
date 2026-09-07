package auth_test

import (
	"bytes"
	"encoding/base64"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"durpdeploy/internal/auth"

	"golang.org/x/crypto/argon2"
)

func TestHashPassword_BasicRoundTrip(t *testing.T) {
	hash, err := auth.HashPassword("hunter2")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if !auth.VerifyPassword(hash, "hunter2") {
		t.Fatal("expected hunter2 to verify against its hash")
	}
	if auth.VerifyPassword(hash, "hunter3") {
		t.Fatal("expected hunter3 to NOT verify against hunter2's hash")
	}
}

func TestHashPassword_DifferentSalts(t *testing.T) {
	h1, err := auth.HashPassword("same")
	if err != nil {
		t.Fatalf("first hash: %v", err)
	}
	h2, err := auth.HashPassword("same")
	if err != nil {
		t.Fatalf("second hash: %v", err)
	}
	if h1 == h2 {
		t.Fatal(
			"expected two hashes of the same password to differ (different random salts)",
		)
	}
	if !auth.VerifyPassword(h1, "same") {
		t.Fatal("expected h1 to verify as same")
	}
	if !auth.VerifyPassword(h2, "same") {
		t.Fatal("expected h2 to verify as same")
	}
}

func TestHashPassword_PHCOutputRecomputesWithPublishedParameters(t *testing.T) {
	// Given
	const password = "correct horse battery staple"
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		t.Fatalf("invalid PHC envelope")
	}
	params := make(map[string]string, 3)
	for _, parameter := range strings.Split(parts[3], ",") {
		name, value, ok := strings.Cut(parameter, "=")
		if !ok || value == "" {
			t.Fatalf("invalid PHC parameter")
		}
		params[name] = value
	}
	memory, err := strconv.ParseUint(params["m"], 10, 32)
	if err != nil {
		t.Fatalf("parse memory: %v", err)
	}
	time, err := strconv.ParseUint(params["t"], 10, 32)
	if err != nil {
		t.Fatalf("parse time: %v", err)
	}
	threads, err := strconv.ParseUint(params["p"], 10, 8)
	if err != nil {
		t.Fatalf("parse threads: %v", err)
	}
	if memory != 65536 || time != 2 || threads != 2 || len(params) != 3 {
		t.Fatalf("unexpected published Argon2id parameters")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		t.Fatalf("decode salt: %v", err)
	}
	digest, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		t.Fatalf("decode digest: %v", err)
	}
	if len(salt) != 16 || len(digest) != 32 {
		t.Fatalf("unexpected salt or digest length")
	}

	// When
	recomputed := argon2.IDKey(
		[]byte(password),
		salt,
		uint32(time),
		uint32(memory),
		uint8(threads),
		uint32(len(digest)),
	)

	// Then
	if !bytes.Equal(recomputed, digest) {
		t.Fatal("recomputed digest does not match PHC digest")
	}
	if !auth.VerifyPassword(hash, password) ||
		auth.VerifyPassword(hash, "wrong password") {
		t.Fatal("public password verification mismatch")
	}
	t.Logf(
		"PHC parsed and independently recomputed: algorithm=argon2id version=19 memory=%d time=%d threads=%d salt_bytes=%d digest_bytes=%d",
		memory,
		time,
		threads,
		len(salt),
		len(digest),
	)
}

func TestHashPassword_PHCFormat(t *testing.T) {
	hash, err := auth.HashPassword("hunter2")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	prefix := "$argon2id$v=19$m=65536,t=2,p=2$"
	if !strings.HasPrefix(hash, prefix) {
		t.Fatalf("expected hash to start with %q, got %q", prefix, hash)
	}
	parts := strings.Split(hash, "$")
	if len(parts) != 6 {
		t.Fatalf("expected 6 $-separated parts, got %d in %q", len(parts), hash)
	}
	if parts[4] == "" {
		t.Fatal("expected non-empty salt part")
	}
	if parts[5] == "" {
		t.Fatal("expected non-empty hash part")
	}
}

func TestVerifyPassword_WrongAlgorithm(t *testing.T) {
	wrong := "$argon2i$v=19$m=65536,t=2,p=2$c29tZXNhbHQ$somehash"
	if auth.VerifyPassword(wrong, "hunter2") {
		t.Fatal("expected argon2i algorithm to be rejected")
	}
}

func TestVerifyPassword_Malformed(t *testing.T) {
	cases := []string{
		"not-a-hash",
		"",
		"$argon2id$",
		"$argon2id$v=19$m=65536,t=2,p=2$",
		"$argon2id$v=19$m=65536,t=2,p=2$salt",
	}
	for _, c := range cases {
		if auth.VerifyPassword(c, "hunter2") {
			t.Fatalf("expected malformed hash %q to be rejected", c)
		}
	}
}

func TestVerifyPassword_TimingSafe(t *testing.T) {
	// Timing safety is provided by crypto/subtle.ConstantTimeCompare in
	// VerifyPassword. A direct unit-test assertion would require
	// nanosecond-level timing control, so we rely on code review and the
	// contract of subtle.ConstantTimeCompare.
	hash, err := auth.HashPassword("hunter2")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if auth.VerifyPassword(hash, "hunter2") != true {
		t.Fatal("expected correct password to verify")
	}
	if auth.VerifyPassword(hash, "hunter3") != false {
		t.Fatal("expected wrong password to fail verification")
	}
}

func TestNewSessionToken_Length(t *testing.T) {
	token, csrf, err := auth.NewSessionToken()
	if err != nil {
		t.Fatalf("new session token: %v", err)
	}
	if len(token) != 64 {
		t.Fatalf("expected token length 64, got %d", len(token))
	}
	if len(csrf) != 32 {
		t.Fatalf("expected csrf length 32, got %d", len(csrf))
	}
}

func TestNewSessionToken_Uniqueness(t *testing.T) {
	tokens := make(map[string]bool)
	for i := 0; i < 100; i++ {
		token, _, err := auth.NewSessionToken()
		if err != nil {
			t.Fatalf("new session token: %v", err)
		}
		if tokens[token] {
			t.Fatalf("duplicate token generated: %q", token)
		}
		tokens[token] = true
	}
}

func TestNewSessionToken_HexEncoding(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]+$`)
	for i := 0; i < 10; i++ {
		token, csrf, err := auth.NewSessionToken()
		if err != nil {
			t.Fatalf("new session token: %v", err)
		}
		if !re.MatchString(token) {
			t.Fatalf("token %q is not lowercase hex", token)
		}
		if !re.MatchString(csrf) {
			t.Fatalf("csrf %q is not lowercase hex", csrf)
		}
	}
}
