//go:build e2e

package api_test

import (
	"encoding/json"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/roles"
)

func seedWarningPasskey(t *testing.T, f permissionFixture, userID int64) {
	t.Helper()
	_, err := f.h.repo.Queries.CreateWebAuthnCredential(t.Context(),
		db.CreateWebAuthnCredentialParams{
			CredentialID: []byte("counter-warning-fixture"), UserID: userID,
			Name: "Counter review key", PublicKey: []byte("private fixture metadata"),
			TransportsJson: "[]", CloneWarning: 1,
		})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOwnPasskeyWarningsAPIWebE2E(t *testing.T) {
	for _, role := range roles.All() {
		t.Run(role, func(t *testing.T) {
			f := newPermissionFixture(t)
			owner := seedAPIUser(t, f.h.repo, "owner@warning.test", role)
			_, token := seedAPIToken(t, f.h.repo, owner.ID)
			seedWarningPasskey(t, f, owner.ID)
			data := f.request(t, "GET", "/api/v1/security/passkeys", "",
				token, "", false, 200)
			var rows []map[string]any
			if err := json.Unmarshal(data, &rows); err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 || len(rows[0]) != 3 ||
				rows[0]["name"] != "Counter review key" ||
				rows[0]["credential_id"] != "Y291bnRlci13YXJuaW5nLWZpeHR1cmU" ||
				rows[0]["clone_warning"] != true {
				t.Fatalf("unexpected warning metadata: %s", data)
			}
			session := memberRoleSession(t, f.h, owner.ID)
			page := f.request(t, "GET", "/settings/security", "", "",
				session, false, 200)
			if !strings.Contains(string(page), "Counter warning") {
				t.Fatal("owner cannot see the passkey warning")
			}
			foreign := seedAPIUser(t, f.h.repo, "other@warning.test", "admin")
			row, otherToken := seedAPIToken(t, f.h.repo, foreign.ID)
			data = f.request(t, "GET", "/api/v1/security/passkeys", "",
				otherToken, "", false, 200)
			if string(data) != "[]\n" {
				t.Fatal("another user can see the owner's passkey")
			}
			if _, err := f.h.repo.ReplaceTokenScope(t.Context(), row,
				repository.TokenScope{Scope: "scoped", Role: "viewer",
					ProjectIDs: []int64{seedProject(t, f.h.repo).ID}}); err != nil {
				t.Fatal(err)
			}
			f.request(t, "GET", "/api/v1/security/passkeys", "",
				otherToken, "", false, 403)
			f.request(t, "GET", "/api/v1/security/passkeys", "",
				"", session, false, 401)
		})
	}
}
