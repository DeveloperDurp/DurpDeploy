//go:build e2e

package api_test

import (
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/mfa"
)

func TestUserRoleValidationAPIWebE2E(t *testing.T) {
	f := newArtifactE2E(t, fakePodman(t))
	target := seedAPIUser(
		t,
		f.h.repo,
		"role-validation@example.test",
		"maintainer",
	)
	targetAccess := *f
	targetAccess.session = memberRoleSession(t, f.h, target.ID)
	_, targetAccess.token = seedAPIToken(t, f.h.repo, target.ID)
	if _, err := mfa.NewChallengeService(mfa.ChallengeServiceConfig{Repository: f.h.repo}).Issue(
		t.Context(), mfa.ChallengeIssue{UserID: target.ID, Purpose: mfa.ChallengePurposeTOTPVerify},
	); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/admin/users/%d", target.ID)
	// A rejected request must not reach even the transaction's account lock write.
	if _, err := f.h.repo.DB.ExecContext(t.Context(), fmt.Sprintf(
		"CREATE TRIGGER reject_role_write BEFORE UPDATE ON users WHEN OLD.id=%d BEGIN SELECT RAISE(ABORT,'account write before role validation'); END",
		target.ID,
	)); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		role any
	}{
		{"omitted", nil}, {"empty", ""}, {"unknown", "owner"},
		{"null", nil}, {"whitespace", " "}, {"uppercase", "ADMIN"},
	} {
		t.Run("invalid/"+test.name, func(t *testing.T) {
			body := map[string]any{
				"name":     "Rejected name",
				"password": "rejected-password-98",
			}
			if test.name != "omitted" {
				body["role"] = test.role
			}
			before := userRoleAuditCount(t, f)
			var envelope struct{ Error string }
			decodeStepLogTest(t, f.api(t, "PUT", path, body, 422), &envelope)
			if envelope.Error != "invalid role" {
				t.Fatalf("wrong validation envelope: %q", envelope.Error)
			}
			checkRejectedUserRole(t, f, &targetAccess, *target, before)
		})
	}
	if _, err := f.h.repo.DB.ExecContext(t.Context(), "DROP TRIGGER reject_role_write"); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"viewer", "deployer", "maintainer", "admin"} {
		t.Run("valid/"+role, func(t *testing.T) {
			checkValidUserRole(t, f, &targetAccess, *target, role)
		})
	}
	if _, err := f.h.repo.DB.ExecContext(t.Context(), "UPDATE users SET role='' WHERE id=?", target.ID); err == nil {
		t.Fatal("storage role constraint did not reject an empty role")
	}
}

func checkRejectedUserRole(
	t *testing.T, f, targetAccess *artifactE2E, before db.User, auditBefore int,
) {
	t.Helper()
	stored, err := f.h.repo.Queries.GetUserByID(t.Context(), before.ID)
	if err != nil || stored != before {
		t.Fatal("rejected role update changed the account")
	}
	for _, table := range []string{"sessions", "mfa_challenges", "api_tokens"} {
		var count int
		if err := f.h.repo.DB.QueryRowContext(t.Context(),
			"SELECT COUNT(*) FROM "+table+" WHERE user_id=?", before.ID,
		).Scan(&count); err != nil || count != 1 {
			t.Fatalf(
				"rejected role changed %s: count=%d error=%v",
				table,
				count,
				err,
			)
		}
	}
	if userRoleAuditCount(t, f) != auditBefore {
		t.Fatal("rejected role update audited as success")
	}
	var response map[string]any
	decodeStepLogTest(
		t,
		f.api(
			t,
			"GET",
			fmt.Sprintf("/api/v1/admin/users/%d", before.ID),
			nil,
			200,
		),
		&response,
	)
	if response["name"] != before.Name || response["role"] != before.Role {
		t.Fatal("rejected account changed in the public API")
	}
	targetAccess.api(t, "GET", "/api/v1/users/me", nil, 200)
	targetAccess.web(t, "GET", "/projects", nil, 200)
}

func userRoleAuditCount(t *testing.T, f *artifactE2E) int {
	t.Helper()
	var count int
	if err := f.h.repo.DB.QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM audit_log WHERE action='update_user'",
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func checkValidUserRole(
	t *testing.T, f, targetAccess *artifactE2E, target db.User, role string,
) {
	t.Helper()
	before := userRoleAuditCount(t, f)
	body := f.api(
		t,
		"PUT",
		fmt.Sprintf("/api/v1/admin/users/%d", target.ID),
		map[string]string{"name": "Accepted name", "role": role},
		200,
	)
	var response struct{ Name, Role string }
	decodeStepLogTest(t, body, &response)
	if response.Name != "Accepted name" || response.Role != role ||
		strings.Contains(string(body), "password_hash") {
		t.Fatal("valid role response changed")
	}
	stored, err := f.h.repo.Queries.GetUserByID(t.Context(), target.ID)
	if err != nil || stored.Role != role ||
		stored.Name != response.Name ||
		stored.PasswordHash != target.PasswordHash {
		t.Fatal(
			"valid role update did not preserve the profile contract",
		)
	}
	if userRoleAuditCount(t, f) != before+1 {
		t.Fatal("valid role update missing successful audit")
	}
	targetAccess.api(t, "GET", "/api/v1/users/me", nil, 200)
}
