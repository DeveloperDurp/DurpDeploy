package handler_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/mfa"
)

func TestAdminMFAReset_ResumesAfterPasswordReauthentication(t *testing.T) {
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
	seedTOTP(t, h, *target.user, box)
	apiToken := seedAPIToken(t, h, target.user.ID)
	if err := h.repo.Queries.MarkSessionReauthenticated(
		context.Background(),
		db.MarkSessionReauthenticatedParams{ID: admin.sessionToken},
	); err != nil {
		t.Fatalf("make admin session stale: %v", err)
	}

	// When
	staleReset := postAdminMFAReset(t, h, adminMFAResetTarget{
		Admin: admin, UserID: target.user.ID,
	})
	defer staleReset.Body.Close()
	if staleReset.StatusCode != http.StatusSeeOther ||
		staleReset.Header.Get("Location") != "/settings/security/reauth" {
		t.Fatalf(
			"stale reset response = %d %q, want 303 reauth redirect",
			staleReset.StatusCode,
			staleReset.Header.Get("Location"),
		)
	}
	reauth, err := admin.client.PostForm(
		h.server+"/settings/security/reauth",
		url.Values{
			"csrf_token": {admin.csrfToken},
			"password":   {"testpass"},
		},
	)
	if err != nil {
		t.Fatalf("POST password reauthentication: %v", err)
	}
	defer reauth.Body.Close()

	// Then
	if reauth.StatusCode != http.StatusSeeOther ||
		reauth.Header.Get("Location") != "/admin/users" {
		t.Fatalf(
			"reauthentication response = %d %q, want reset redirect",
			reauth.StatusCode,
			reauth.Header.Get("Location"),
		)
	}
	if countSecurityRows(
		t,
		h,
		"SELECT COUNT(*) FROM mfa_totp WHERE user_id = ?",
		target.user.ID,
	) != 0 {
		t.Fatal("reauthentication did not reset target MFA")
	}
	assertAPITokenWorks(t, h, apiToken)
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
	if len(entries) != 1 || !entries[0].EntityID.Valid ||
		entries[0].EntityID.Int64 != target.user.ID {
		t.Fatalf("reset audit rows = %#v, want target reset entry", entries)
	}
}
