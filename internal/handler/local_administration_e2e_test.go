//go:build e2e

package handler_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

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

type localAdministrationFixture struct {
	h     [2]*projectHarness
	round int
}

type localAdminPair struct {
	users  [2]db.User
	tokens [2]string
}

type localAdminChange struct {
	targetID                        int64
	method, surface, role, password string
}

type localAdminResult struct {
	redirect string
	status   int
	body     []byte
	err      error
}

func TestLocalAdministrationAPIWebE2E(t *testing.T) {
	for _, backend := range []string{"SQLite", "PostgreSQL", "SQLServer"} {
		t.Run(backend, func(t *testing.T) {
			f := newLocalAdministrationFixture(t, backend)
			t.Run(
				"sole-and-handoff",
				func(t *testing.T) { f.checkSoleAdministrator(t) },
			)
			for _, surface := range []string{"API", "Web", "Mixed"} {
				for _, mutation := range []string{"demote", "delete", "mixed"} {
					t.Run(surface+"/"+mutation, func(t *testing.T) {
						f.checkPublicAdminRace(t, surface, mutation)
					})
				}
			}
			for _, mutation := range []string{"demote", "delete", "mixed"} {
				t.Run("authenticated-in-flight/"+mutation, func(t *testing.T) {
					f.checkAuthenticatedAdminRace(t, mutation)
				})
			}
			t.Run(
				"delete-rollback",
				func(t *testing.T) { f.checkAdminDeleteRollback(t, backend) },
			)
			t.Run(
				"missing-lock",
				func(t *testing.T) { f.checkMissingAdministrationLock(t) },
			)
		})
	}
}

func newLocalAdministrationFixture(
	t *testing.T,
	backend string,
) *localAdministrationFixture {
	t.Helper()
	dsn := projectUpdateDSN(t, backend)
	f := &localAdministrationFixture{}
	for index := range f.h {
		conn, err := migrate.Run(dsn)
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
		srv := httptest.NewServer(server.NewRouter(
			repo,
			rnr,
			cron.NewParser(
				cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow,
			),
			handler.NewAuthHandler(repo),
		))
		t.Cleanup(srv.Close)
		f.h[index] = &projectHarness{t: t, repo: repo, server: srv}
	}
	return f
}

func (f *localAdministrationFixture) pair(
	t *testing.T,
	secondRole string,
) localAdminPair {
	t.Helper()
	// Reset only fixture roles between completed scenarios, outside the public operation under test.
	if _, err := f.h[0].repo.DB.ExecContext(t.Context(), "UPDATE users SET role='viewer' WHERE role='admin'"); err != nil {
		t.Fatal(err)
	}
	f.round++
	var pair localAdminPair
	for index, role := range []string{"admin", secondRole} {
		h := f.h[index]
		h.sess = seedSessionAs(t, h.repo, h.server.URL,
			fmt.Sprintf("admin-%d-%d@example.test", f.round, index), role)
		pair.users[index] = *h.sess.user
		pair.tokens[index] = accountUpdateToken(t, h.repo, h.sess.user.ID)
	}
	return pair
}

func requestLocalAdminChange(
	ctx context.Context,
	h *projectHarness,
	token string,
	change localAdminChange,
) localAdminResult {
	path := fmt.Sprintf("/admin/users/%d", change.targetID)
	data, err := json.Marshal(map[string]string{
		"name": "Edited administrator", "role": change.role, "password": change.password,
	})
	if err != nil {
		return localAdminResult{err: err}
	}
	contentType := "application/json"
	if change.surface == "Web" {
		contentType = "application/x-www-form-urlencoded"
		data = []byte(
			url.Values{"name": {"Edited administrator"}, "role": {change.role},
				"password": {
					change.password,
				}, "password_confirmation": {change.password},
				"csrf_token": {h.sess.csrfToken}}.Encode(),
		)
	} else {
		path = "/api/v1" + path
		if change.method == "DELETE" {
			data = nil
		}
	}
	req, err := http.NewRequestWithContext(
		ctx,
		change.method,
		h.server.URL+path,
		bytes.NewReader(data),
	)
	if err != nil {
		return localAdminResult{err: err}
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-CSRF-Token", h.sess.csrfToken)
	response, err := h.sess.client.Do(req)
	if err != nil {
		return localAdminResult{err: err}
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	return localAdminResult{
		status:   response.StatusCode,
		body:     body,
		err:      err,
		redirect: response.Header.Get("Location"),
	}
}

func requireLocalAdminStatus(t *testing.T, result localAdminResult, want int) {
	t.Helper()
	if result.err != nil || result.status != want {
		t.Fatalf(
			"local mutation: status=%d want=%d err=%v body=%s",
			result.status,
			want,
			result.err,
			result.body,
		)
	}
}

func (f *localAdministrationFixture) checkSoleAdministrator(t *testing.T) {
	t.Helper()
	pair := f.pair(t, "viewer")
	user := pair.users[0]
	if _, err := mfa.NewChallengeService(mfa.ChallengeServiceConfig{Repository: f.h[0].repo}).Issue(
		t.Context(), mfa.ChallengeIssue{UserID: user.ID, Purpose: mfa.ChallengePurposeTOTPVerify}); err != nil {
		t.Fatal(err)
	}
	for _, surface := range []string{"API", "Web"} {
		before := localAdminAuditCount(t, f.h[0].repo)
		result := requestLocalAdminChange(
			t.Context(),
			f.h[0],
			pair.tokens[0],
			localAdminChange{
				targetID: user.ID, method: "PUT", surface: surface, role: "viewer", password: "rejected-password-105",
			},
		)
		requireLocalAdminStatus(t, result, 422)
		if !strings.Contains(
			string(result.body),
			auth.ErrLastAdministrator.Error(),
		) ||
			strings.Contains(string(result.body), "rejected-password-105") {
			t.Fatal("rejected demotion lost its error or exposed the password")
		}
		if surface == "Web" &&
			!strings.Contains(string(result.body), "Edited administrator") {
			t.Fatal("rejected demotion lost the web draft")
		}
		assertLocalAccountUnchanged(t, f.h[0].repo, user)
		if localAdminAuditCount(t, f.h[0].repo) != before {
			t.Fatal("rejected demotion audited as a successful change")
		}
	}
	// Existing self-deletion contracts remain distinct from final-admin validation.
	for surface, status := range map[string]int{"API": 403, "Web": 422} {
		requireLocalAdminStatus(
			t,
			requestLocalAdminChange(
				t.Context(),
				f.h[0],
				pair.tokens[0],
				localAdminChange{
					targetID: user.ID,
					method:   "DELETE",
					surface:  surface,
				},
			),
			status,
		)
	}
	// Promote the replacement before handing over the old administrator's role.
	requireLocalAdminStatus(
		t,
		requestLocalAdminChange(
			t.Context(),
			f.h[0],
			pair.tokens[0],
			localAdminChange{
				targetID: pair.users[1].ID,
				method:   "PUT",
				role:     "admin",
			},
		),
		200,
	)
	requireLocalAdminStatus(
		t,
		requestLocalAdminChange(
			t.Context(),
			f.h[0],
			pair.tokens[0],
			localAdminChange{
				targetID: user.ID,
				method:   "PUT",
				role:     "viewer",
			},
		),
		200,
	)
	assertUsableLocalAdministrator(t, f, pair)
}

func (f *localAdministrationFixture) checkPublicAdminRace(
	t *testing.T,
	surface, mutation string,
) {
	t.Helper()
	pair := f.pair(t, "admin")
	changes := [2]localAdminChange{
		{
			targetID: pair.users[0].ID,
			method:   "PUT",
			role:     "viewer",
			surface:  surface,
		},
		{
			targetID: pair.users[1].ID,
			method:   "PUT",
			role:     "viewer",
			surface:  surface,
		},
	}
	callers, tokens := f.h, pair.tokens
	if surface == "Mixed" {
		changes[0].surface, changes[1].surface = "API", "Web"
	}
	switch mutation {
	case "delete":
		changes[0].method, changes[1].method = "DELETE", "DELETE"
		changes[0].targetID, changes[1].targetID = pair.users[1].ID, pair.users[0].ID
	case "mixed":
		changes[1].method = "DELETE"
		second := *f.h[1]
		second.sess = f.h[0].sess
		callers[1], tokens[1] = &second, pair.tokens[0]
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	start := make(chan struct{})
	results := make(chan localAdminResult, 2)
	before := localAdminAuditCount(t, f.h[0].repo)
	for index := range changes {
		go func() {
			<-start
			results <- requestLocalAdminChange(ctx, callers[index], tokens[index], changes[index])
		}()
	}
	close(start)
	successes := 0
	completed := [2]localAdminResult{<-results, <-results}
	for _, result := range completed {
		if localAdminMutationSucceeded(t, result) {
			successes++
		}
	}
	if successes != 1 || localAdminAuditCount(t, f.h[0].repo) != before+1 {
		t.Fatalf(
			"race committed %d mutations or lost audit integrity",
			successes,
		)
	}
	assertUsableLocalAdministrator(t, f, pair)
}

func (f *localAdministrationFixture) checkAuthenticatedAdminRace(
	t *testing.T,
	mutation string,
) {
	t.Helper()
	pair := f.pair(t, "admin")
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	start := make(chan struct{})
	results := make(chan error, 2)
	for index := range f.h {
		go func() {
			<-start
			results <- f.applyAuthenticatedAdminChange(ctx, pair, index, mutation)
		}()
	}
	close(start)
	successes, rejected := 0, 0
	completed := [2]error{<-results, <-results}
	for _, err := range completed {
		if err == nil {
			successes++
		} else if errors.Is(err, auth.ErrLastAdministrator) {
			rejected++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || rejected != 1 {
		t.Fatalf("in-flight race: success=%d rejected=%d", successes, rejected)
	}
	assertUsableLocalAdministrator(t, f, pair)
}

func localAdminMutationSucceeded(t *testing.T, result localAdminResult) bool {
	t.Helper()
	if result.err != nil {
		t.Fatal(result.err)
	}
	if result.status >= 200 && result.status < 300 ||
		result.status == 303 && result.redirect == "/admin/users" {
		return true
	}
	if result.status == 303 && result.redirect == "/login" {
		return false
	}
	if result.status != 401 && result.status != 403 && result.status != 422 {
		t.Fatalf("unexpected race response: %d %s", result.status, result.body)
	}
	return false
}

func (f *localAdministrationFixture) applyAuthenticatedAdminChange(
	ctx context.Context, pair localAdminPair, index int, mutation string,
) error {
	id, caller := pair.users[index].ID, index
	if mutation == "delete" {
		id = pair.users[1-index].ID
	} else if mutation == "mixed" {
		caller = 0
	}
	request := auth.SetUser(
		httptest.NewRequest("PUT", "/", nil).WithContext(ctx),
		&pair.users[caller],
	)
	if mutation == "delete" || mutation == "mixed" && index == 1 {
		return auth.DeleteAccount(request.Context(), f.h[index].repo, id)
	}
	_, err := auth.UpdateAccount(request.Context(), f.h[index].repo,
		db.UpdateUserParams{ID: id, Name: "In-flight edit", Role: "viewer"}, "")
	return err
}

func (f *localAdministrationFixture) checkAdminDeleteRollback(
	t *testing.T,
	backend string,
) {
	t.Helper()
	pair := f.pair(t, "admin")
	user := pair.users[1]
	if _, err := mfa.NewChallengeService(mfa.ChallengeServiceConfig{Repository: f.h[1].repo}).Issue(
		t.Context(), mfa.ChallengeIssue{UserID: user.ID, Purpose: mfa.ChallengePurposeTOTPVerify}); err != nil {
		t.Fatal(err)
	}
	removeFault := accountUpdateFault(
		t,
		f.h[0].repo,
		backend,
		"sessions",
		user.ID,
	)
	requireLocalAdminStatus(
		t,
		requestLocalAdminChange(t.Context(), f.h[0], pair.tokens[0],
			localAdminChange{targetID: user.ID, method: "DELETE"}),
		500,
	)
	removeFault()
	assertLocalAccountUnchanged(t, f.h[0].repo, user)
	requireLocalAdminStatus(
		t,
		requestLocalAdminChange(t.Context(), f.h[0], pair.tokens[0],
			localAdminChange{targetID: user.ID, method: "DELETE"}),
		204,
	)
	assertUsableLocalAdministrator(t, f, pair)
	// Keep the API's existing idempotent missing-delete contract.
	requireLocalAdminStatus(
		t,
		requestLocalAdminChange(t.Context(), f.h[0], pair.tokens[0],
			localAdminChange{targetID: user.ID, method: "DELETE"}),
		204,
	)
}

func (f *localAdministrationFixture) checkMissingAdministrationLock(
	t *testing.T,
) {
	t.Helper()
	pair := f.pair(t, "viewer")
	if _, err := f.h[0].repo.DB.ExecContext(t.Context(), "DELETE FROM local_administration_lock"); err != nil {
		t.Fatal(err)
	}
	requireLocalAdminStatus(
		t,
		requestLocalAdminChange(
			t.Context(),
			f.h[0],
			pair.tokens[0],
			localAdminChange{
				targetID: pair.users[0].ID,
				method:   "PUT",
				role:     "admin",
			},
		),
		500,
	)
	actual, err := f.h[0].repo.Queries.GetUserByID(
		t.Context(),
		pair.users[0].ID,
	)
	if err != nil || actual != pair.users[0] {
		t.Fatal("unavailable coordination state allowed an account write")
	}
	if _, err := f.h[0].repo.DB.ExecContext(t.Context(), "INSERT INTO local_administration_lock (id,version) VALUES (1,0)"); err != nil {
		t.Fatal(err)
	}
}

func assertLocalAccountUnchanged(
	t *testing.T,
	repo *repository.Repository,
	user db.User,
) {
	t.Helper()
	actual, err := repo.Queries.GetUserByID(t.Context(), user.ID)
	if err != nil || actual != user {
		t.Fatal("rejected local mutation changed the account")
	}
	for _, table := range []string{"sessions", "mfa_challenges", "api_tokens"} {
		var count int
		if err := repo.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table+" WHERE user_id=?", user.ID).Scan(&count); err != nil ||
			count != 1 {
			t.Fatalf(
				"rejected local mutation changed %s: count=%d err=%v",
				table,
				count,
				err,
			)
		}
	}
}

func localAdminAuditCount(t *testing.T, repo *repository.Repository) int {
	t.Helper()
	var count int
	if err := repo.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM audit_log WHERE action IN ('update_user','delete_user')").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func assertUsableLocalAdministrator(
	t *testing.T,
	f *localAdministrationFixture,
	pair localAdminPair,
) {
	t.Helper()
	count, err := f.h[0].repo.Queries.CountAdministrators(t.Context())
	if err != nil || count != 1 {
		t.Fatalf("remaining administrators=%d err=%v", count, err)
	}
	for index, user := range pair.users {
		current, err := f.h[index].repo.Queries.GetUserByID(
			t.Context(),
			user.ID,
		)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if current.Role == "admin" {
			if !auth.VerifyPassword(current.PasswordHash, "testpass") {
				t.Fatal("remaining administrator has no usable password")
			}
			f.h[index].accountUpdateRequest(
				t,
				pair.tokens[index],
				"GET",
				"/api/v1/admin/users",
				nil,
				false,
				200,
			)
			return
		}
	}
	t.Fatal("the remaining administrator is not a surviving fixture account")
}
