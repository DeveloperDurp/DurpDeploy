//go:build e2e

package handler_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/mfa"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/oidc/oidctest"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/secret"
	"durpdeploy/internal/server"
	"durpdeploy/internal/testutil"
	"github.com/robfig/cron/v3"
)

type roleChangeFixture struct {
	h              *authHarness
	admin          *projectHarness
	fixture        oidcCallbackFixture
	box            *secret.Box
	owner, backend string
}

func TestRoleChangeRevocationAPIWebE2E(t *testing.T) {
	for _, backend := range []string{"SQLite", "PostgreSQL", "SQLServer"} {
		t.Run(backend, func(t *testing.T) {
			conn, err := migrate.Run(projectUpdateDSN(t, backend))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { testutil.Close(t, conn) })
			repo := repository.New(conn)
			rnr := runner.NewWithPodmanBinaryForTest(
				repo,
				runner.NewLogBroker(),
				setupTestPodman(t),
			)
			t.Cleanup(rnr.KillAll)
			authHandler := handler.NewAuthHandler(repo)
			srv := httptest.NewServer(
				server.NewRouter(
					repo,
					rnr,
					cron.NewParser(
						cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow,
					),
					authHandler,
				),
			)
			t.Cleanup(srv.Close)
			h := &authHarness{
				t:           t,
				repo:        repo,
				server:      srv.URL,
				authHandler: authHandler,
			}
			box := configureMFA(t, h, mfa.Config{})
			fixture := configureOIDCCallback(t, h)
			admin := &projectHarness{
				t:      t,
				repo:   repo,
				server: srv,
				sess:   seedSession(t, repo, srv.URL, "admin"),
			}
			owner := accountUpdateToken(t, repo, admin.sess.user.ID)
			f := roleChangeFixture{
				h:       h,
				admin:   admin,
				fixture: fixture,
				box:     box,
				owner:   owner,
				backend: backend,
			}
			for _, transport := range []string{"API", "OIDC"} {
				for _, change := range []string{"name", "promote", "demote"} {
					t.Run(transport+"/"+change, func(t *testing.T) {
						f.checkRoleChange(t, transport, change)
					})
				}
			}
		})
	}
}

func (f roleChangeFixture) checkRoleChange(
	t *testing.T,
	transport, change string,
) {
	t.Helper()
	h, admin, box := f.h, f.admin, f.box
	repo, srv := h.repo, admin.server
	beforeRole, afterRole := "viewer", "viewer"
	switch change {
	case "promote":
		afterRole = "admin"
	case "demote":
		beforeRole = "admin"
	}
	target := seedSessionAs(
		t,
		repo,
		srv.URL,
		transport+"-"+change+"@example.test",
		beforeRole,
	)
	original := *target.user
	second := fmt.Sprintf("role-second-%d", original.ID)
	if _, err := repo.Queries.CreateSession(t.Context(), db.CreateSessionParams{ID: second, UserID: original.ID, CsrfToken: "second-csrf", ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	code := seedTOTP(t, h, original, box)
	pending := []pendingLogin{
		issueLoginMFAChallenge(t, h, original.ID),
		issueLoginMFAChallenge(t, h, original.ID),
	}
	token := accountUpdateToken(t, repo, original.ID)
	f.checkRoleChangeRollbacks(t, original, transport, change, afterRole)
	f.applyRoleChange(t, original, transport, afterRole, true)
	wantSessions, wantWeb := f.assertRoleChange(
		t,
		original,
		afterRole,
		transport,
		change,
	)
	for _, session := range []string{target.sessionToken, second} {
		assertSessionStatus(
			t,
			srv.URL+"/projects",
			&http.Cookie{Name: "session", Value: session},
			wantWeb,
		)
	}
	admin.accountUpdateRequest(
		t,
		token,
		"GET",
		"/api/v1/users/me",
		nil,
		false,
		200,
	)
	wantAdmin := 403
	if afterRole == "admin" {
		wantAdmin = 200
	}
	admin.accountUpdateRequest(
		t,
		token,
		"GET",
		"/api/v1/admin/users",
		nil,
		false,
		wantAdmin,
	)
	if change != "name" {
		f.assertRevokedChallenges(t, original, pending, code, wantSessions)
	}
}

func (f roleChangeFixture) applyRoleChange(
	t *testing.T,
	original db.User,
	transport, afterRole string,
	wantSuccess bool,
) {
	t.Helper()
	admin, owner, fixture, h := f.admin, f.owner, f.fixture, f.h
	if transport == "API" {
		accountUpdatePut(
			t,
			admin,
			owner,
			"API",
			db.UpdateUserParams{
				ID:   original.ID,
				Name: "Updated name",
				Role: afterRole,
			},
			"",
			200,
		)
		return
	}
	fixture.provider.SetClaims(
		oidctest.Claims{
			Subject: fmt.Sprintf(
				"role-%d",
				original.ID,
			),
			Email:         original.Email,
			EmailVerified: true,
			Groups: []string{
				"fixture-" + afterRole,
			},
		},
	)
	response := fixture.begin(t, h).callback(t)
	defer response.Body.Close()
	if wantSuccess {
		assertOIDCCallbackSuccess(t, response)
	} else {
		assertOIDCCallbackFailure(t, response)
	}
}

func (f roleChangeFixture) checkRoleChangeRollbacks(
	t *testing.T,
	original db.User,
	transport, change, afterRole string,
) {
	t.Helper()
	h, backend := f.h, f.backend
	repo := h.repo
	// A failed OIDC invalidation must also roll back synchronized claims.
	if transport == "OIDC" && change != "name" {
		for _, table := range []string{"mfa_challenges", "sessions"} {
			remove := accountUpdateFault(
				t,
				repo,
				backend,
				table,
				original.ID,
			)
			f.applyRoleChange(t, original, transport, afterRole, false)
			remove()
			unchanged, err := repo.Queries.GetUserByID(
				t.Context(),
				original.ID,
			)
			if err != nil || unchanged != original {
				t.Fatalf(
					"failed sync changed user: %+v: %v",
					unchanged,
					err,
				)
			}
			assertRoleAuthCounts(t, h, original.ID, 2, 2)
		}
	}
}

func (f roleChangeFixture) assertRoleChange(
	t *testing.T,
	original db.User,
	afterRole, transport, change string,
) (int, int) {
	t.Helper()
	h := f.h
	repo := h.repo
	updated, err := repo.Queries.GetUserByID(
		t.Context(),
		original.ID,
	)
	wantName := "Updated name"
	if transport == "OIDC" {
		wantName = strings.ToLower(original.Email)
	}
	if err != nil || updated.Name != wantName ||
		updated.Role != afterRole ||
		updated.PasswordHash != original.PasswordHash {
		t.Fatalf(
			"wrong synchronized account: %+v: %v",
			updated,
			err,
		)
	}
	wantSessions, wantChallenges, wantWeb := 0, 0, 303
	if change == "name" {
		wantSessions, wantChallenges, wantWeb = 2, 2, 200
	}
	if transport == "OIDC" {
		wantSessions++ // The fresh OIDC login issues its own session.
	}
	assertRoleAuthCounts(
		t,
		h,
		original.ID,
		wantSessions,
		wantChallenges,
	)
	return wantSessions, wantWeb
}

func (f roleChangeFixture) assertRevokedChallenges(
	t *testing.T,
	original db.User,
	pending []pendingLogin,
	code string,
	wantSessions int,
) {
	t.Helper()
	h, srv := f.h, f.admin.server
	for _, challenge := range pending {
		response, err := challenge.client.PostForm(
			srv.URL+"/login/mfa/totp",
			url.Values{
				"code":       {code},
				"csrf_token": {challenge.csrf},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 422 ||
			hasCookie(response.Cookies(), "session") {
			t.Fatalf(
				"revoked challenge issued authentication: %d",
				response.StatusCode,
			)
		}
	}
	assertRoleAuthCounts(
		t,
		h,
		original.ID,
		wantSessions,
		0,
	)
}

func assertRoleAuthCounts(
	t *testing.T,
	h *authHarness,
	userID int64,
	sessions, challenges int,
) {
	t.Helper()
	if got := sessionCount(t, h, userID); got != sessions {
		t.Fatalf("sessions=%d, want %d", got, sessions)
	}
	var got int
	if err := h.repo.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM mfa_challenges WHERE user_id=?", userID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != challenges {
		t.Fatalf("challenges=%d, want %d", got, challenges)
	}
}
