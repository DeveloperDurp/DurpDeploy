package handler_test

import (
	"context"
	"encoding/json"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/oidc"
	"durpdeploy/internal/oidc/oidctest"
)

func TestOIDCCallback_linksExistingUserAndIssuesSingleAuditedSession(
	t *testing.T,
) {
	// Given
	h := newAuthHarness(t)
	existing := h.seedUser(t, "fixture@example.test", "hunter2")
	fixture := configureOIDCCallback(t, h)
	flow := fixture.begin(t, h)

	// When
	response := flow.callback(t)

	// Then
	assertOIDCCallbackSuccess(t, response)
	assertOIDCStateCounts(t, h, [4]int{1, 1, 1, 0})
	identity, err := h.repo.Queries.GetOIDCIdentity(
		context.Background(),
		db.GetOIDCIdentityParams{
			Issuer: fixture.provider.URL(), Subject: "fixture-subject",
		},
	)
	if err != nil {
		t.Fatalf("get linked OIDC identity: %v", err)
	}
	if identity.UserID != existing.ID {
		t.Fatalf("linked user ID = %d, want %d", identity.UserID, existing.ID)
	}
	assertOIDCSessionAudit(t, h, existing.ID)
}

func TestOIDCCallback_JITProvisionsMappedUser_whenIdentityIsNew(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	fixture := configureOIDCCallback(t, h)
	fixture.provider.SetClaims(oidctest.Claims{
		Subject:       "jit-subject",
		Email:         "jit@example.test",
		EmailVerified: true,
		Groups:        []string{"fixture-deployer"},
	})
	flow := fixture.begin(t, h)

	// When
	response := flow.callback(t)

	// Then
	assertOIDCCallbackSuccess(t, response)
	assertOIDCStateCounts(t, h, [4]int{1, 1, 1, 0})
	user, err := h.repo.Queries.GetUserByEmail(
		context.Background(),
		"jit@example.test",
	)
	if err != nil {
		t.Fatalf("get JIT-provisioned user: %v", err)
	}
	if user.PasswordHash != "" || user.Role != string(oidc.RoleDeployer) {
		t.Fatalf("JIT user = %#v, want empty password and deployer role", user)
	}
}

func TestOIDCCallback_appliesConfiguredViewerRoleMapping(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	fixture := configureOIDCCallback(t, h)
	fixture.provider.SetClaims(oidctest.Claims{
		Subject:       "viewer-subject",
		Email:         "viewer@example.test",
		EmailVerified: true,
		Groups:        []string{"fixture-viewer"},
	})
	flow := fixture.begin(t, h)

	// When
	response := flow.callback(t)

	// Then
	assertOIDCCallbackSuccess(t, response)
	user, err := h.repo.Queries.GetUserByEmail(
		context.Background(),
		"viewer@example.test",
	)
	if err != nil {
		t.Fatalf("get viewer-mapped user: %v", err)
	}
	if user.Role != string(oidc.RoleViewer) {
		t.Fatalf("mapped role = %q, want %q", user.Role, oidc.RoleViewer)
	}
}

func assertOIDCSessionAudit(t *testing.T, h *authHarness, userID int64) {
	t.Helper()
	if got := sessionCount(t, h, userID); got != 1 {
		t.Fatalf("browser session count = %d, want 1", got)
	}
	entries, err := h.repo.Queries.ListAuditLogs(context.Background(), 2)
	if err != nil {
		t.Fatalf("list OIDC audit entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(entries))
	}
	entry := entries[0]
	if !entry.UserID.Valid || entry.UserID.Int64 != userID ||
		entry.Action != "mfa_login_factor" || entry.EntityType != "user" ||
		!entry.EntityID.Valid || entry.EntityID.Int64 != userID ||
		!entry.Details.Valid {
		t.Fatalf("OIDC audit entry = %#v", entry)
	}
	details := map[string]string{}
	if err := json.Unmarshal(
		[]byte(entry.Details.String),
		&details,
	); err != nil {
		t.Fatalf("decode OIDC audit details: %v", err)
	}
	if len(details) != 3 || details["factor"] != "oidc" ||
		details["user_agent"] != oidcCallbackUserAgent || details["ip"] == "" {
		t.Fatalf("OIDC audit details = %#v", details)
	}
}
