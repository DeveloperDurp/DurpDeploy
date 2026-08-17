package handler_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/mfa"
)

func TestOIDCReauthCallback_refreshesOnlyBoundSessionAndResumesContinuation(
	t *testing.T,
) {
	// Given
	h := newAuthHarness(t)
	box := configureMFA(t, h, mfa.Config{})
	current := seedSessionAs(t, h.repo, h.server, "admin@example.test", "admin")
	other := seedSecuritySession(t, h, current.user)
	target := seedSessionAs(
		t,
		h.repo,
		h.server,
		"target@example.test",
		"deployer",
	)
	seedTOTP(t, h, *target.user, box)
	if err := h.repo.Queries.MarkSessionReauthenticated(
		context.Background(),
		db.MarkSessionReauthenticatedParams{ID: current.sessionToken},
	); err != nil {
		t.Fatalf("make bound session stale: %v", err)
	}
	if err := mfa.NewChallengeService(mfa.ChallengeServiceConfig{
		Repository: h.repo,
	}).ReplaceAdminMFAResetContinuation(
		context.Background(),
		current.user.ID,
		current.sessionToken,
		fmt.Sprintf(
			`{"target_user_id":%d,"reason":"lost_device"}`,
			target.user.ID,
		),
	); err != nil {
		t.Fatalf("create reauthentication continuation: %v", err)
	}
	otherBefore := readSession(t, h, other.sessionToken).ReauthenticatedAt
	fixture := configureOIDCCallback(t, h)
	seedOIDCReauthIdentity(
		t,
		h,
		current.user.ID,
		fixture.provider.URL(),
		"fixture-subject",
	)
	flow := beginOIDCReauth(t, h, fixture, current)

	// When
	response := flow.callback(t, current)

	// Then
	assertOIDCReauthSuccess(t, response, "/admin/users")
	if !readSession(t, h, current.sessionToken).ReauthenticatedAt.Valid {
		t.Fatal(
			"same-subject OIDC reauthentication did not refresh the bound session",
		)
	}
	if after := readSession(
		t,
		h,
		other.sessionToken,
	).ReauthenticatedAt; after != otherBefore {
		t.Fatal("same-subject OIDC reauthentication refreshed another session")
	}
	if countSecurityRows(
		t,
		h,
		"SELECT COUNT(*) FROM mfa_totp WHERE user_id = ?",
		target.user.ID,
	) != 0 {
		t.Fatal("OIDC reauthentication did not resume the bound continuation")
	}
	if countSecurityRows(
		t,
		h,
		"SELECT COUNT(*) FROM sessions WHERE user_id = ?",
		current.user.ID,
	) != 2 {
		t.Fatal("OIDC reauthentication created a browser session")
	}
}

func TestOIDCReauthCallback_rejectsDifferentSessionUserOrSubject(t *testing.T) {
	tests := []struct {
		name             string
		useOtherSession  bool
		useDifferentUser bool
		differentSubject bool
	}{
		{name: "different session", useOtherSession: true},
		{name: "different user", useDifferentUser: true},
		{name: "different subject", differentSubject: true},
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
			callbackSession := current
			if test.useOtherSession {
				callbackSession = seedSecuritySession(t, h, current.user)
			}
			if test.useDifferentUser {
				callbackSession = seedSessionAs(
					t,
					h.repo,
					h.server,
					"other@example.test",
					"admin",
				)
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
			if test.differentSubject {
				fixture.provider.SetClaims(oidcReauthClaims(
					"other-subject",
					fixture.provider.Now(),
				))
			}

			// When
			response := flow.callback(t, callbackSession)

			// Then
			assertOIDCCallbackFailure(t, response)
			if readSession(
				t,
				h,
				current.sessionToken,
			).ReauthenticatedAt != (sql.NullInt64{}) {
				t.Fatal(
					"mismatched OIDC reauthentication refreshed the bound session",
				)
			}
		})
	}
}
