package handler_test

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/mfa"
)

func TestAdminMFAReset_ConsumesContinuationOnceAfterFactorReauthentication(
	t *testing.T,
) {
	// Given
	h := newAuthHarness(t)
	box := configureMFA(t, h, mfa.Config{})
	admin := seedSessionAs(t, h.repo, h.server, "admin@example.com", "admin")
	target := seedSessionAs(
		t,
		h.repo,
		h.server,
		"target@example.com",
		"deployer",
	)
	adminCode := seedTOTP(t, h, *admin.user, box)
	seedTOTP(t, h, *target.user, box)
	staleReset := beginStaleAdminMFAReset(t, h, adminMFAResetTarget{
		Admin: admin, UserID: target.user.ID,
	})
	defer staleReset.Body.Close()
	password := postPasswordReauthentication(t, h, admin, "testpass", true)
	defer password.Body.Close()
	if password.StatusCode != http.StatusOK {
		t.Fatalf(
			"password reauthentication = %d, want factor prompt",
			password.StatusCode,
		)
	}
	factorBody, err := io.ReadAll(password.Body)
	if err != nil {
		t.Fatalf("read factor prompt: %v", err)
	}
	factor := securityHiddenValues(t, string(factorBody))
	factor.Set("csrf_token", admin.csrfToken)
	factor.Set("code", adminCode)

	// When
	first, err := admin.client.PostForm(
		h.server+"/settings/security/reauth/totp",
		factor,
	)
	if err != nil {
		t.Fatalf("POST factor reauthentication: %v", err)
	}
	defer first.Body.Close()
	second, err := admin.client.PostForm(
		h.server+"/settings/security/reauth/totp",
		factor,
	)
	if err != nil {
		t.Fatalf("replay factor reauthentication: %v", err)
	}
	defer second.Body.Close()

	// Then
	if first.StatusCode != http.StatusSeeOther ||
		first.Header.Get("Location") != "/admin/users" {
		t.Fatalf("first factor response = %d %q", first.StatusCode,
			first.Header.Get("Location"))
	}
	if second.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("replayed factor response = %d, want 422", second.StatusCode)
	}
	if countSecurityRows(
		t,
		h,
		"SELECT COUNT(*) FROM mfa_totp WHERE user_id = ?",
		target.user.ID,
	) != 0 {
		t.Fatal("first factor completion did not reset target MFA")
	}
	entries, err := h.repo.Queries.ListAuditLogsFiltered(
		context.Background(),
		db.ListAuditLogsFilteredParams{
			PageLimit: 10,
			FAction:   sql.NullString{String: "mfa_admin_reset", Valid: true},
		},
	)
	if err != nil {
		t.Fatalf("list reset audits: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("reset audit rows after replay = %d, want 1", len(entries))
	}
}
