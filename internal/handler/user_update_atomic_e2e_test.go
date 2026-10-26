//go:build e2e

package handler_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/mfa"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/server"
	"durpdeploy/internal/testutil"
	"github.com/robfig/cron/v3"
)

type accountUpdateCase struct {
	h                     *projectHarness
	original              db.User
	update                db.UpdateUserParams
	token, password, mode string
}

func TestUserUpdateAtomicAPIWebE2E(t *testing.T) {
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
			srv := httptest.NewServer(
				server.NewRouter(
					repo,
					rnr,
					cron.NewParser(
						cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow,
					),
					handler.NewAuthHandler(repo),
				),
			)
			t.Cleanup(srv.Close)
			h := &projectHarness{t: t, repo: repo, server: srv}
			h.sess = seedSession(t, repo, srv.URL, "admin")
			owner := accountUpdateToken(t, repo, h.sess.user.ID)
			for _, surface := range []string{"API", "Web"} {
				for _, mode := range []string{"profile", "password", "role", "combined"} {
					t.Run(surface+"/"+mode, func(t *testing.T) {
						h.checkAccountUpdate(t, owner, backend, surface, mode)
					})
				}
			}
		})
	}
}

func (h *projectHarness) checkAccountUpdate(
	t *testing.T, owner, backend, surface, mode string,
) {
	t.Helper()
	repo, srv := h.repo, h.server
	target := seedSessionAs(
		t,
		repo,
		srv.URL,
		strings.ToLower(surface+"-"+mode)+"@example.test",
		"deployer",
	)
	if _, err := mfa.NewChallengeService(mfa.ChallengeServiceConfig{Repository: repo}).Issue(t.Context(), mfa.ChallengeIssue{UserID: target.user.ID, Purpose: mfa.ChallengePurposeTOTPVerify}); err != nil {
		t.Fatal(err)
	}
	token := accountUpdateToken(t, repo, target.user.ID)
	role, password := "deployer", ""
	if mode == "role" || mode == "combined" {
		role = "viewer"
	}
	if mode == "password" || mode == "combined" {
		password = "new-password-96"
	}
	original := *target.user
	name := original.Name
	if mode == "profile" || mode == "combined" {
		name = "Updated account"
	}
	c := accountUpdateCase{
		h: h, original: original,
		update: db.UpdateUserParams{ID: original.ID, Name: name, Role: role},
		token:  token, password: password, mode: mode,
	}
	c.checkRollbacks(t, owner, backend, surface)
	before := accountUpdateAuditCount(t, repo)
	want := 200
	if surface == "Web" {
		want = 303
	}
	accountUpdatePut(
		t,
		h,
		owner,
		surface,
		c.update,
		password,
		want,
	)
	c.assertState(t, true)
	if got := accountUpdateAuditCount(t, repo); got != before+1 {
		t.Fatalf(
			"successful audit count %d -> %d",
			before,
			got,
		)
	}
}

func (c accountUpdateCase) assertState(t *testing.T, changed bool) {
	t.Helper()
	h, original, mode, token := c.h, c.original, c.mode, c.token
	repo := h.repo
	user, err := repo.Queries.GetUserByID(
		t.Context(),
		original.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	wantBrowser := 1
	if changed {
		c.assertCommittedState(t, user)
		if mode != "profile" {
			wantBrowser = 0
		}
	} else if user != original {
		t.Fatalf("failed request changed account: %+v", user)
	}
	for table, want := range map[string]int{"sessions": wantBrowser, "mfa_challenges": wantBrowser, "api_tokens": 1} {
		var count int
		if err := repo.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table+" WHERE user_id=?", original.ID).Scan(&count); err != nil ||
			count != want {
			t.Fatalf(
				"%s count=%d want=%d: %v",
				table,
				count,
				want,
				err,
			)
		}
	}
	h.accountUpdateRequest(
		t,
		token,
		"GET",
		"/api/v1/users/me",
		nil,
		false,
		200,
	)
}

func (c accountUpdateCase) assertCommittedState(t *testing.T, user db.User) {
	t.Helper()
	original, password := c.original, c.password
	name, role := c.update.Name, c.update.Role
	if user.Name != name ||
		user.Role != role {
		t.Fatalf(
			"wrong committed profile: %+v",
			user,
		)
	}
	if password != "" {
		valid := auth.VerifyPassword(
			user.PasswordHash,
			password,
		)
		if !valid ||
			!strings.HasPrefix(
				user.PasswordHash,
				"$argon2id$",
			) {
			t.Fatalf(
				"password did not commit",
			)
		}
	} else if user.PasswordHash != original.PasswordHash {
		t.Fatal("blank password changed hash")
	}
}

func (c accountUpdateCase) checkRollbacks(
	t *testing.T, owner, backend, surface string,
) {
	t.Helper()
	h, original, password, mode := c.h, c.original, c.password, c.mode
	repo := h.repo
	caseT := t
	for _, fault := range []string{"password", "mfa_challenges", "sessions"} {
		if mode == "profile" ||
			fault == "password" && password == "" {
			continue
		}
		t.Run("rollback-"+fault, func(t *testing.T) {
			remove := accountUpdateFault(
				t,
				repo,
				backend,
				fault,
				original.ID,
			)
			defer remove()
			before := accountUpdateAuditCount(t, repo)
			accountUpdatePut(
				t,
				h,
				owner,
				surface,
				c.update,
				password,
				500,
			)
			c.assertState(caseT, false)
			if got := accountUpdateAuditCount(t, repo); got != before {
				t.Fatalf(
					"failed update audited as success: %d -> %d",
					before,
					got,
				)
			}
		})
	}
}

func accountUpdateToken(
	t *testing.T,
	repo *repository.Repository,
	userID int64,
) string {
	t.Helper()
	token, prefix, hash, err := auth.MintApiToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Queries.CreateApiToken(t.Context(), db.CreateApiTokenParams{ID: fmt.Sprintf("account-%d", userID), UserID: userID, Name: "account fixture", TokenPrefix: prefix, TokenHash: hash, Scope: "global"}); err != nil {
		t.Fatal(err)
	}
	return token
}

func accountUpdateAuditCount(t *testing.T, repo *repository.Repository) int {
	t.Helper()
	var count int
	if err := repo.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM audit_log WHERE action='update_user'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func accountUpdatePut(
	t *testing.T,
	h *projectHarness,
	token, surface string,
	user db.UpdateUserParams,
	password string,
	want int,
) {
	t.Helper()
	path := fmt.Sprintf("/admin/users/%d", user.ID)
	if surface == "API" {
		body, err := json.Marshal(
			map[string]string{
				"name":     user.Name,
				"role":     user.Role,
				"password": password,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		h.accountUpdateRequest(
			t,
			token,
			"PUT",
			"/api/v1"+path,
			body,
			false,
			want,
		)
		return
	}
	form := url.Values{
		"name":                  {user.Name},
		"role":                  {user.Role},
		"password":              {password},
		"password_confirmation": {password},
		"csrf_token":            {h.csrfToken()},
	}
	h.accountUpdateRequest(
		t,
		"",
		"PUT",
		path,
		[]byte(form.Encode()),
		true,
		want,
	)
}

func (h *projectHarness) accountUpdateRequest(
	t *testing.T,
	token, method, path string,
	body []byte,
	web bool,
	want int,
) {
	t.Helper()
	req, err := http.NewRequestWithContext(
		t.Context(),
		method,
		h.server.URL+path,
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if web {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-CSRF-Token", h.csrfToken())
	} else {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := h.authedClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != want {
		t.Fatalf(
			"%s %s status=%d want=%d body=%s",
			method,
			path,
			resp.StatusCode,
			want,
			data,
		)
	}
}

func accountUpdateFault(
	t *testing.T,
	repo *repository.Repository,
	backend, fault string,
	userID int64,
) func() {
	t.Helper()
	table, column, operation := "users", "id", "UPDATE OF password_hash"
	if fault != "password" {
		table, column, operation = fault, "user_id", "DELETE"
	}
	var statements, drops []string
	switch backend {
	case "SQLite":
		statements = []string{
			fmt.Sprintf(
				"CREATE TRIGGER fail_account_update BEFORE %s ON %s WHEN OLD.%s=%d BEGIN SELECT RAISE(ABORT,'injected account failure'); END",
				operation,
				table,
				column,
				userID,
			),
		}
		drops = []string{"DROP TRIGGER fail_account_update"}
	case "PostgreSQL":
		result := "OLD"
		if fault == "password" {
			result = "NEW"
		}
		statements = []string{
			fmt.Sprintf(
				"CREATE FUNCTION fail_account_update() RETURNS TRIGGER AS $$ BEGIN IF OLD.%s=%d THEN RAISE EXCEPTION 'injected account failure'; END IF; RETURN %s; END; $$ LANGUAGE plpgsql",
				column,
				userID,
				result,
			),
			fmt.Sprintf(
				"CREATE TRIGGER fail_account_update BEFORE %s ON %s FOR EACH ROW EXECUTE FUNCTION fail_account_update()",
				operation,
				table,
			),
		}
		drops = []string{
			"DROP TRIGGER fail_account_update ON " + table,
			"DROP FUNCTION fail_account_update()",
		}
	case "SQLServer":
		action, rows, condition := "DELETE", "deleted", ""
		if fault == "password" {
			action, rows, condition = "UPDATE", "inserted", "UPDATE(password_hash) AND "
		}
		statements = []string{
			fmt.Sprintf(
				"CREATE TRIGGER fail_account_update ON %s AFTER %s AS BEGIN IF %sEXISTS (SELECT 1 FROM %s WHERE %s=%d) THROW 51000, 'injected account failure', 1; END",
				table,
				action,
				condition,
				rows,
				column,
				userID,
			),
		}
		drops = []string{"DROP TRIGGER fail_account_update"}
	}
	// Keep independent failure stages on distinct database schema objects.
	triggerName := fmt.Sprintf("fail_account_%d_%s", userID, fault)
	for _, statement := range statements {
		if _, err := repo.DB.ExecContext(t.Context(), strings.ReplaceAll(
			statement, "fail_account_update", triggerName,
		)); err != nil {
			t.Fatal(err)
		}
	}
	return func() {
		for _, statement := range drops {
			if _, err := repo.DB.ExecContext(t.Context(), strings.ReplaceAll(
				statement, "fail_account_update", triggerName,
			)); err != nil {
				t.Error(err)
			}
		}
	}
}
