package handler_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/mfa"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

func TestLogin_WebAuthnCompletesPendingChallenge(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	configureMFA(t, h, mfa.Config{WebAuthn: mfa.WebAuthnConfig{
		Enabled: true, Origin: "https://example.org", RPID: "example.org",
	}})
	user := h.seedUser(t, "passkey@example.com", "hunter2")
	credentialID, publicKey, assertion := officialUVAssertion(t)
	handle := bytes.Repeat([]byte{1}, 32)
	if _, err := h.repo.Queries.CreateWebAuthnUser(
		context.Background(),
		db.CreateWebAuthnUserParams{
			UserID: user.ID, RpID: "example.org", UserHandle: handle,
		},
	); err != nil {
		t.Fatalf("create WebAuthn identity: %v", err)
	}
	if _, err := h.repo.Queries.CreateWebAuthnCredential(
		context.Background(),
		db.CreateWebAuthnCredentialParams{
			CredentialID:   credentialID,
			UserID:         user.ID,
			Name:           "test credential",
			PublicKey:      publicKey,
			TransportsJson: "[]",
			Flags: int64(protocol.FlagUserPresent | protocol.FlagUserVerified |
				protocol.FlagBackupEligible | protocol.FlagBackupState),
			AttestationType:   "none",
			AttestationFormat: "none",
		},
	); err != nil {
		t.Fatalf("create WebAuthn credential: %v", err)
	}
	pending := loginPending(t, h, user.Email)

	// When
	begin, err := http.NewRequest(
		http.MethodPost,
		h.server+"/login/mfa/webauthn/begin",
		nil,
	)
	if err != nil {
		t.Fatalf("new assertion begin request: %v", err)
	}
	begin.Header.Set("X-CSRF-Token", pending.csrf)
	beginResponse, err := pending.client.Do(begin)
	if err != nil {
		t.Fatalf("begin assertion: %v", err)
	}
	beginResponse.Body.Close()
	if beginResponse.StatusCode != http.StatusOK ||
		beginResponse.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("begin assertion response = %d %q", beginResponse.StatusCode,
			beginResponse.Header.Get("Cache-Control"))
	}

	// The official assertion is tied to its signed server-side SessionData.
	session := webauthn.SessionData{
		RelyingPartyID:       "example.org",
		Challenge:            "7x3rpW3OSPZ0pEfM9juVmSWM6HZI5cOW8u8ModpGDjs",
		UserID:               handle,
		AllowedCredentialIDs: [][]byte{credentialID},
		UserVerification:     protocol.VerificationRequired,
	}
	ceremony, err := json.Marshal(session)
	if err != nil {
		t.Fatalf("marshal assertion ceremony: %v", err)
	}
	if _, err := h.repo.DB.ExecContext(
		context.Background(),
		"UPDATE mfa_challenges SET ceremony_json = ? WHERE user_id = ?",
		string(ceremony),
		user.ID,
	); err != nil {
		t.Fatalf("replace assertion ceremony: %v", err)
	}
	finish, err := http.NewRequest(
		http.MethodPost,
		h.server+"/login/mfa/webauthn/finish",
		bytes.NewReader(assertion),
	)
	if err != nil {
		t.Fatalf("new assertion finish request: %v", err)
	}
	finish.Header.Set("Content-Type", "application/json")
	finish.Header.Set("X-CSRF-Token", pending.csrf)
	finishResponse, err := pending.client.Do(finish)
	if err != nil {
		t.Fatalf("finish assertion: %v", err)
	}
	defer finishResponse.Body.Close()

	// Then
	if finishResponse.StatusCode != http.StatusOK ||
		finishResponse.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("finish assertion response = %d %q", finishResponse.StatusCode,
			finishResponse.Header.Get("Cache-Control"))
	}
	pendingCookiesCleared(t, finishResponse)
	if sessionCount(t, h, user.ID) != 1 {
		t.Fatal("WebAuthn completion did not create one final session")
	}
}

func officialUVAssertion(t *testing.T) ([]byte, []byte, []byte) {
	t.Helper()
	credentialID := officialUVCredentialID(t)
	publicKey := decodeHex(
		t,
		"a50102032620012158203b8176b7504489cc593046d7988abb7905a742de6ac2cdc748a873c663e90cb12258201436d5edc9a75f23999eef9d5950a5c2455514ee1014084720f841a06b828a11",
	)
	response := map[string]string{
		"authenticatorData": base64.RawURLEncoding.EncodeToString(
			decodeHex(
				t,
				"bfabc37432958b063360d3ad6461c9c4735ae7f8edd46592a5e0f01452b2e4b50d00000000",
			),
		),
		"clientDataJSON": base64.RawURLEncoding.EncodeToString(
			decodeHex(
				t,
				"7b2274797065223a22776562617574686e2e676574222c226368616c6c656e6765223a22377833727057334f53505a307045664d396a75566d53574d36485a4935634f573875384d6f647047446a73222c226f726967696e223a2268747470733a2f2f6578616d706c652e6f7267222c2263726f73734f726967696e223a66616c73657d",
			),
		),
		"signature": base64.RawURLEncoding.EncodeToString(
			decodeHex(
				t,
				"304502203ecef83fb12a0cae7841055f9f87103a99fd14b424194bbf06c4623d3ee6e3fd022100d2ace346db262b1374a6b70faa51f518a42ddca13a4125ce6f5052a75bac9fb6",
			),
		),
	}
	body, err := json.Marshal(map[string]any{
		"id":       base64.RawURLEncoding.EncodeToString(credentialID),
		"rawId":    base64.RawURLEncoding.EncodeToString(credentialID),
		"type":     "public-key",
		"response": response,
	})
	if err != nil {
		t.Fatalf("marshal assertion: %v", err)
	}
	return credentialID, publicKey, body
}

func officialUVCredentialID(t *testing.T) []byte {
	t.Helper()
	cache, err := exec.Command("go", "env", "GOMODCACHE").Output()
	if err != nil {
		t.Fatalf("resolve module cache: %v", err)
	}
	source, err := os.ReadFile(filepath.Join(
		strings.TrimSpace(string(cache)),
		"github.com/go-webauthn/webauthn@v0.17.4/protocol/specification_vectors_e2e_test.go",
	))
	if err != nil {
		t.Fatalf("read official WebAuthn vector: %v", err)
	}
	const marker = "name:                        \"NoneES256LongCredentialID\""
	start := bytes.Index(source, []byte(marker))
	if start < 0 {
		t.Fatal("official WebAuthn vector is unavailable")
	}
	const field = "credentialID:                \""
	start += bytes.Index(source[start:], []byte(field)) + len(field)
	end := bytes.IndexByte(source[start:], '"')
	if end < 0 {
		t.Fatal("official WebAuthn credential ID is malformed")
	}
	return decodeHex(t, string(source[start:start+end]))
}

func decodeHex(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := hex.DecodeString(value)
	if err != nil {
		t.Fatalf("decode vector data: %v", err)
	}
	return decoded
}
