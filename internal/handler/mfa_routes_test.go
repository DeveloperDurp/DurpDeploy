package handler_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/mfa"
)

func TestMFARoutesRegisterPublicAndProtectedHandlers(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	configureMFA(t, h, mfa.Config{})
	user := seedSessionAs(t, h.repo, h.server, "routes@example.com", "admin")
	target := seedSessionAs(
		t,
		h.repo,
		h.server,
		"target@example.com",
		"deployer",
	)
	markSessionFresh(t, h, user.sessionToken)

	// When / Then
	for _, path := range []string{
		"/login/mfa",
		"/settings/security/reauth",
	} {
		response, err := user.client.Get(h.server + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		response.Body.Close()
		if response.StatusCode == http.StatusNotFound ||
			response.StatusCode == http.StatusMethodNotAllowed {
			t.Errorf("GET %s was not registered: %d", path, response.StatusCode)
		}
	}

	for _, path := range []string{
		"/login/mfa/totp",
		"/login/mfa/recovery",
		"/login/mfa/webauthn/begin",
		"/login/mfa/webauthn/finish",
		"/login/mfa/cancel",
	} {
		response, err := http.PostForm(h.server+path, url.Values{})
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		response.Body.Close()
		if response.StatusCode == http.StatusNotFound ||
			response.StatusCode == http.StatusMethodNotAllowed {
			t.Errorf(
				"POST %s was not registered: %d",
				path,
				response.StatusCode,
			)
		}
	}

	for _, path := range []string{
		"/settings/security/reauth",
		"/settings/security/reauth/totp",
		"/settings/security/reauth/recovery",
		"/settings/security/reauth/webauthn/begin",
		"/settings/security/reauth/webauthn/finish",
		"/settings/security/totp/begin",
		"/settings/security/totp/confirm",
		"/settings/security/passkeys/begin",
		"/settings/security/passkeys/finish",
		"/settings/security/passkeys/rename",
		"/settings/security/passkeys/delete",
		"/settings/security/recovery/regenerate",
		"/settings/security/disable",
		"/admin/users/" + strconv.FormatInt(target.user.ID, 10) + "/mfa-reset",
	} {
		response, err := user.client.PostForm(
			h.server+path,
			url.Values{
				"csrf_token": {user.csrfToken},
				"reason":     {"lost_device"},
			},
		)
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		response.Body.Close()
		if response.StatusCode == http.StatusNotFound ||
			response.StatusCode == http.StatusMethodNotAllowed {
			t.Errorf(
				"POST %s was not registered: %d",
				path,
				response.StatusCode,
			)
		}
	}
}

func TestViewerSecurityWritesAreLimitedToOwnFreshSession(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	configureMFA(t, h, mfa.Config{})
	viewer := seedSessionAs(t, h.repo, h.server, "viewer@example.com", "viewer")
	target := seedSessionAs(
		t,
		h.repo,
		h.server,
		"target@example.com",
		"deployer",
	)
	markSessionFresh(t, h, viewer.sessionToken)

	securityForm := url.Values{"csrf_token": {viewer.csrfToken}}

	// When
	securityResponse, err := viewer.client.PostForm(
		h.server+"/settings/security/totp/begin",
		securityForm,
	)
	if err != nil {
		t.Fatalf("POST own security route: %v", err)
	}
	defer securityResponse.Body.Close()

	projectResponse, err := viewer.client.PostForm(
		h.server+"/projects",
		url.Values{"csrf_token": {viewer.csrfToken}},
	)
	if err != nil {
		t.Fatalf("POST unrelated route: %v", err)
	}
	defer projectResponse.Body.Close()
	crossUserResponse, err := viewer.client.PostForm(
		h.server+"/admin/users/"+strconv.FormatInt(
			target.user.ID,
			10,
		)+"/mfa-reset",
		url.Values{"csrf_token": {viewer.csrfToken}},
	)
	if err != nil {
		t.Fatalf("POST cross-user security route: %v", err)
	}
	defer crossUserResponse.Body.Close()
	missingCSRFResponse, err := viewer.client.PostForm(
		h.server+"/settings/security/totp/begin",
		url.Values{},
	)
	if err != nil {
		t.Fatalf("POST own security route without CSRF: %v", err)
	}
	defer missingCSRFResponse.Body.Close()

	// Then
	if securityResponse.StatusCode != http.StatusOK {
		t.Errorf(
			"own security write = %d, want 200",
			securityResponse.StatusCode,
		)
	}
	if projectResponse.StatusCode != http.StatusForbidden {
		t.Errorf(
			"unrelated viewer write = %d, want 403",
			projectResponse.StatusCode,
		)
	}
	if crossUserResponse.StatusCode != http.StatusForbidden {
		t.Errorf(
			"cross-user viewer write = %d, want 403",
			crossUserResponse.StatusCode,
		)
	}
	if missingCSRFResponse.StatusCode != http.StatusForbidden {
		t.Errorf(
			"missing CSRF security write = %d, want 403",
			missingCSRFResponse.StatusCode,
		)
	}

	if err := h.repo.Queries.MarkSessionReauthenticated(
		context.Background(),
		db.MarkSessionReauthenticatedParams{ID: viewer.sessionToken},
	); err != nil {
		t.Fatalf("make viewer session stale: %v", err)
	}
	staleResponse, err := viewer.client.PostForm(
		h.server+"/settings/security/disable",
		url.Values{"csrf_token": {viewer.csrfToken}},
	)
	if err != nil {
		t.Fatalf("POST stale security route: %v", err)
	}
	defer staleResponse.Body.Close()
	if staleResponse.StatusCode != http.StatusSeeOther {
		t.Errorf(
			"stale security write = %d, want 303",
			staleResponse.StatusCode,
		)
	}
	entries, err := h.repo.Queries.ListAuditLogsFiltered(
		context.Background(),
		db.ListAuditLogsFilteredParams{
			PageLimit: 10,
			FAction:   sql.NullString{String: "mfa_disable", Valid: true},
		},
	)
	if err != nil {
		t.Fatalf("list stale security audits: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("stale security audits = %d, want 0", len(entries))
	}
}

func markSessionFresh(t *testing.T, h *authHarness, sessionID string) {
	t.Helper()
	if err := h.repo.Queries.MarkSessionReauthenticated(
		context.Background(),
		db.MarkSessionReauthenticatedParams{
			ID: sessionID,
			ReauthenticatedAt: sql.NullInt64{
				Int64: time.Now().Unix(),
				Valid: true,
			},
		},
	); err != nil {
		t.Fatalf("mark session fresh: %v", err)
	}
}
