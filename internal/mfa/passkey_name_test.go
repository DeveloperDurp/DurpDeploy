package mfa

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-webauthn/webauthn/webauthn"
)

func TestNormalizePasskeyName(t *testing.T) {
	for _, test := range []struct {
		name, input, want string
		valid             bool
	}{
		{"trim", " \tWork laptop\n", "Work laptop", true},
		{"ascii boundary", strings.Repeat("a", 255), strings.Repeat("a", 255), true},
		{"wide boundary", strings.Repeat("🔑", 127) + "a", strings.Repeat("🔑", 127) + "a", true},
		{"long ascii", strings.Repeat("a", 256), "", false},
		{"long wide", strings.Repeat("🔑", 128), "", false},
		{"whitespace", " \t\n", "", false},
		{"invalid encoding", "key\xff", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Given: a name at the user-input boundary.
			// When: it is normalized for all supported databases.
			name, err := NormalizePasskeyName(test.input)

			// Then: accepted names use the same UTF-16 limit as the browser.
			if name != test.want || (err == nil) != test.valid {
				t.Fatalf("name=%q error=%v", name, err)
			}
			if err != nil && !errors.Is(err, ErrPasskeyName) {
				t.Fatalf("unexpected validation error: %v", err)
			}
		})
	}
}

func TestPasskeyPersistenceRejectsOverlengthNames(t *testing.T) {
	// Given: a valid user and credential with an oversized name.
	_, repo, _, user := newFactorStoreFixture(t)
	store := NewFactorStore(FactorStoreConfig{Repository: repo})
	params := credentialParams(user.ID, []byte("key"))
	params.Name = strings.Repeat("🔑", 128)

	// When: each persistence entry point receives the name.
	_, conversionErr := CredentialToDB(webauthn.Credential{
		ID: params.CredentialID, PublicKey: params.PublicKey,
	}, user.ID, params.Name)
	_, createErr := store.CreateCredential(t.Context(), params)
	_, transactionErr := store.CreateCredentialWith(
		t.Context(),
		repo.Queries,
		params,
	)

	// Then: no entry point relies on a database-specific truncation error.
	for _, err := range []error{conversionErr, createErr, transactionErr} {
		if !errors.Is(err, ErrPasskeyName) {
			t.Fatalf("expected passkey-name validation, got %v", err)
		}
	}
	rows, err := repo.Queries.ListWebAuthnCredentialsByUserID(
		t.Context(),
		user.ID,
	)
	if err != nil || len(rows) != 0 {
		t.Fatalf(
			"invalid credential persisted: count=%d error=%v",
			len(rows),
			err,
		)
	}
}
