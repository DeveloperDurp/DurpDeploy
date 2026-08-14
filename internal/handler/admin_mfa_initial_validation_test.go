package handler_test

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"durpdeploy/internal/mfa"
)

func TestAdminMFAReset_RejectsInvalidInitialRequestWithoutSideEffects(
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
	seedTOTP(t, h, *target.user, box)

	// When / Then
	for _, test := range []struct {
		name       string
		userID     int64
		reason     string
		wantStatus int
	}{
		{
			name:       "unknown target",
			userID:     999999,
			reason:     "lost_device",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "invalid reason",
			userID:     target.user.ID,
			reason:     "untrusted",
			wantStatus: http.StatusUnprocessableEntity,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			response, err := admin.client.PostForm(
				h.server+"/admin/users/"+strconv.FormatInt(test.userID, 10)+
					"/mfa-reset",
				url.Values{
					"csrf_token": {admin.csrfToken},
					"reason":     {test.reason},
				},
			)
			if err != nil {
				t.Fatalf("POST invalid reset request: %v", err)
			}
			defer response.Body.Close()
			if response.StatusCode != test.wantStatus {
				t.Fatalf(
					"invalid reset response = %d, want %d",
					response.StatusCode,
					test.wantStatus,
				)
			}
			assertNoAdminMFAReset(t, h, target.user.ID)
		})
	}

	var continuations int
	if err := h.repo.DB.QueryRowContext(
		context.Background(),
		`SELECT COUNT(*) FROM mfa_challenges
WHERE user_id = ? AND purpose = ?`,
		admin.user.ID,
		string(mfa.ChallengePurposeAdminMFAReset),
	).Scan(&continuations); err != nil {
		t.Fatalf("count reset continuations: %v", err)
	}
	if continuations != 0 {
		t.Fatalf("invalid reset continuations = %d, want 0", continuations)
	}
}
