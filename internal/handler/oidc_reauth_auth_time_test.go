package handler_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"durpdeploy/internal/db"
)

func TestOIDCReauthCallback_rejectsMissingOrStaleAuthTime(t *testing.T) {
	tests := []struct {
		name     string
		authTime time.Time
	}{
		{name: "missing auth_time"},
		{
			name:     "stale auth_time",
			authTime: time.Date(2026, 8, 14, 11, 59, 59, 0, time.UTC),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			h := newAuthHarness(t)
			current := seedSessionAs(
				t,
				h.repo,
				h.server,
				"admin@example.test",
				"admin",
			)
			if err := h.repo.Queries.MarkSessionReauthenticated(
				context.Background(),
				db.MarkSessionReauthenticatedParams{ID: current.sessionToken},
			); err != nil {
				t.Fatalf("make bound session stale: %v", err)
			}
			fixture := configureOIDCCallback(t, h)
			seedOIDCReauthIdentity(
				t,
				h,
				current.user.ID,
				fixture.provider.URL(),
				"fixture-subject",
			)
			flow := beginOIDCReauth(t, h, fixture, current)
			fixture.provider.SetClaims(oidcReauthClaims(
				"fixture-subject",
				test.authTime,
			))

			// When
			response := flow.callback(t, current)

			// Then
			assertOIDCCallbackFailure(t, response)
			if readSession(
				t,
				h,
				current.sessionToken,
			).ReauthenticatedAt != (sql.NullInt64{}) {
				t.Fatal(
					"stale or missing auth_time refreshed the bound session",
				)
			}
		})
	}
}
