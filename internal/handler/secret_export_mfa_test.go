package handler_test

import (
	"net/http"
	"net/url"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/mfa"
)

func TestSecretExportRequiresBoundMFAChallenge(t *testing.T) {
	h := newAuthHarness(t)
	box := configureMFA(t, h, mfa.Config{})
	current := seedSession(t, h.repo, h.server, "admin")
	code := seedTOTP(t, h, *current.user, box)
	project, err := h.repo.Queries.CreateProject(
		t.Context(),
		db.CreateProjectParams{Name: "export-mfa"},
	)
	if err != nil {
		t.Fatal(err)
	}
	first, _, err := h.repo.NewSecretExport(
		t.Context(),
		db.CreateSecretExportParams{
			UserID: current.user.ID, SessionID: current.sessionToken, Scope: "project", ScopeID: project.ID,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := h.repo.NewSecretExport(
		t.Context(),
		db.CreateSecretExportParams{
			UserID: current.user.ID, SessionID: current.sessionToken, Scope: "project", ScopeID: project.ID,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	password := postSecurityForm(
		t,
		h,
		current,
		"/settings/security/reauth",
		url.Values{
			"password":  {"testpass"},
			"export_id": {first.ID},
		},
		h.authHandler.SecurityReauthPost,
	)
	if password.Code != http.StatusOK {
		t.Fatalf("password = %d", password.Code)
	}
	row, err := h.repo.SecretExport(t.Context(), first.ID, current.user.ID)
	if err != nil || row.Approved != 0 {
		t.Fatal("password alone approved an MFA export")
	}
	challenge := securityHiddenValues(t, password.Body.String())
	challenge.Set("code", "invalid")
	failed := postSecurityForm(
		t,
		h,
		current,
		"/settings/security/reauth/totp",
		challenge,
		h.authHandler.SecurityReauthTOTPPost,
	)
	if failed.Code != 422 {
		t.Fatalf("invalid factor = %d", failed.Code)
	}
	challenge.Set("code", code)
	// An extra request field must not switch the challenge to another export.
	challenge.Set("export_id", second.ID)
	success := postSecurityForm(
		t,
		h,
		current,
		"/settings/security/reauth/totp",
		challenge,
		h.authHandler.SecurityReauthTOTPPost,
	)
	if success.Code != 303 ||
		success.Header().Get("Location") != "/admin/secret-exports/"+first.ID {
		t.Fatalf(
			"factor response = %d %s",
			success.Code,
			success.Header().Get("Location"),
		)
	}
	row, err = h.repo.SecretExport(t.Context(), second.ID, current.user.ID)
	if err != nil || row.Approved != 0 {
		t.Fatal("factor approved an unrelated export")
	}
	row, err = h.repo.SecretExport(t.Context(), first.ID, current.user.ID)
	if err != nil || row.Approved != 1 {
		t.Fatal("factor did not approve its bound export")
	}
	replay := postSecurityForm(
		t,
		h,
		current,
		"/settings/security/reauth/totp",
		challenge,
		h.authHandler.SecurityReauthTOTPPost,
	)
	if replay.Code != 422 {
		t.Fatalf("factor replay = %d", replay.Code)
	}
}
