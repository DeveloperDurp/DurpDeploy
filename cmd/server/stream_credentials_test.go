package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"github.com/stretchr/testify/require"
)

func TestStreamAccessRechecksDurableCredentialsAndRoles(t *testing.T) {
	for _, mode := range []string{"session", "token"} {
		for _, change := range []string{"valid", "invalidate", "expire", "demote", "demote with membership"} {
			t.Run(mode+"/"+change, func(t *testing.T) {
				checkStreamCredentialChange(t, mode, change)
			})
		}
	}
}

func checkStreamCredentialChange(t *testing.T, mode, change string) {
	t.Helper()
	// Given: middleware authenticated an administrator before a stream opened.
	f := newStreamFixture(t)
	request := httptest.NewRequest("GET", "/", nil).
		WithContext(t.Context())
	var authenticated *http.Request
	capture := http.HandlerFunc(
		func(_ http.ResponseWriter, r *http.Request) {
			authenticated = r
		},
	)
	var middleware http.Handler
	if mode == "session" {
		request.AddCookie(
			&http.Cookie{Name: "session", Value: "timeout-session"},
		)
		middleware = auth.AuthMiddleware(f.repo)(capture)
	} else {
		request.Header.Set(
			"Authorization",
			"Bearer "+streamTestToken("timeout"),
		)
		middleware = auth.ApiTokenMiddleware(f.repo)(capture)
	}
	middleware.ServeHTTP(httptest.NewRecorder(), request)
	require.NotNil(t, authenticated)
	for _, route := range []streamRoute{f.paths[0], f.paths[4]} {
		require.True(
			t,
			auth.CanStreamDeployment(
				authenticated,
				f.repo,
				route.id,
			),
		)
	}
	// When: durable credentials or roles change while the request context remains stale.
	var statement string
	switch change {
	case "invalidate":
		if mode == "session" {
			statement = "DELETE FROM sessions WHERE id='timeout-session'"
		} else {
			statement = "UPDATE api_tokens SET revoked_at=1 WHERE id='timeout-token'"
		}
	case "expire":
		if mode == "session" {
			statement = "UPDATE sessions SET expires_at=0 WHERE id='timeout-session'"
		} else {
			statement = "UPDATE api_tokens SET expires_at=0 WHERE id='timeout-token'"
		}
	case "demote", "demote with membership":
		statement = "UPDATE users SET role='deployer' WHERE id=1"
	}
	if statement != "" {
		_, err := f.repo.DB.ExecContext(t.Context(), statement)
		require.NoError(t, err)
	}
	if change == "demote with membership" {
		require.NoError(
			t,
			f.repo.Queries.AddProjectMember(
				t.Context(),
				db.AddProjectMemberParams{
					ProjectID: 1, UserID: 1, Role: "deployer",
				},
			),
		)
	}
	// Then: both deployment kinds reject invalid access and retain current member access.
	require.Equal(
		t,
		"admin",
		auth.UserFromContext(authenticated.Context()).Role,
	)
	want := change == "valid" || change == "demote with membership"
	for _, route := range []streamRoute{f.paths[0], f.paths[4]} {
		require.Equal(
			t,
			want,
			auth.CanStreamDeployment(
				authenticated,
				f.repo,
				route.id,
			),
		)
	}
}
