//go:build e2e

package api_test

import (
	"database/sql"
	"encoding/json"
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
	"durpdeploy/internal/repository"
	"durpdeploy/internal/server"
	"github.com/robfig/cron/v3"
)

const passwordChangeJSON = `{"current_password":"testpass","new_password":"New-password-141","password_confirmation":"New-password-141"}`

func passwordChangeForm() url.Values {
	return url.Values{
		"current_password":      {"testpass"},
		"new_password":          {"New-password-141"},
		"password_confirmation": {"New-password-141"},
	}
}

func passwordFreshSession(
	t *testing.T,
	f permissionFixture,
	userID int64,
) string {
	t.Helper()
	session := memberRoleSession(t, f.h, userID)
	if err := f.h.repo.Queries.MarkSessionReauthenticated(t.Context(),
		db.MarkSessionReauthenticatedParams{
			ID: session,
			ReauthenticatedAt: sql.NullInt64{
				Int64: time.Now().Unix(),
				Valid: true,
			},
		}); err != nil {
		t.Fatal(err)
	}
	return session
}

func passwordHTTP(
	t *testing.T,
	f permissionFixture,
	path, body, session, csrf string,
) (int, http.Header, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(
		t.Context(),
		"POST",
		f.server.URL+path,
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(&http.Cookie{Name: "session", Value: session})
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, response.Header, string(data)
}

func TestSelfPasswordChangeAPIWebE2E(t *testing.T) {
	for _, surface := range []string{"API", "web"} {
		for _, role := range []string{"viewer", "deployer", "maintainer", "admin"} {
			t.Run(surface+"/"+role, func(t *testing.T) {
				checkSelfPasswordChange(t, surface, role)
			})
		}
	}
}

func checkSelfPasswordChange(t *testing.T, surface, role string) {
	t.Helper()
	f := newPermissionFixture(t)
	user := seedAPIUser(t, f.h.repo, "owner@example.test", role)
	other := seedAPIUser(
		t,
		f.h.repo,
		"other@example.test",
		"viewer",
	)
	_, token := seedAPIToken(t, f.h.repo, user.ID)
	session := passwordFreshSession(t, f, user.ID)
	otherSession := memberRoleSession(t, f.h, other.ID)
	challenges := mfa.NewChallengeService(
		mfa.ChallengeServiceConfig{Repository: f.h.repo},
	)
	pending, err := challenges.Issue(
		t.Context(),
		mfa.ChallengeIssue{
			UserID: user.ID, SessionID: session,
			Purpose: mfa.ChallengePurposeTOTPVerify,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	f.request(
		t,
		"GET",
		"/settings/security",
		"",
		"",
		session,
		false,
		200,
	)
	submitSelfPasswordChange(t, f, surface, token, session, other.ID)
	updated, err := f.h.repo.Queries.GetUserByID(
		t.Context(),
		user.ID,
	)
	if err != nil ||
		auth.VerifyPassword(updated.PasswordHash, "testpass") ||
		!auth.VerifyPassword(
			updated.PasswordHash,
			"New-password-141",
		) {
		t.Fatalf("password update did not persist: %v", err)
	}
	if _, err := f.h.repo.Queries.GetSession(
		t.Context(),
		db.GetSessionParams{ID: session},
	); err != sql.ErrNoRows {
		t.Fatalf("owner session survived: %v", err)
	}
	if _, err := challenges.ResolveBound(
		t.Context(),
		mfa.ChallengeBinding{
			Token:     pending.Token,
			CSRF:      pending.CSRF,
			UserID:    user.ID,
			SessionID: session,
			Purpose:   mfa.ChallengePurposeTOTPVerify,
		},
	); err == nil {
		t.Fatal("pending challenge survived")
	}
	f.request(
		t,
		"GET",
		"/api/v1/users/me",
		"",
		token,
		"",
		false,
		200,
	)
	f.request(
		t,
		"GET",
		"/settings/security",
		"",
		"",
		otherSession,
		false,
		200,
	)
	unchanged, err := f.h.repo.Queries.GetUserByID(
		t.Context(),
		other.ID,
	)
	if err != nil || unchanged.PasswordHash != other.PasswordHash {
		t.Fatal("another account changed")
	}
	var count int
	if err := f.h.repo.DB.QueryRowContext(
		t.Context(),
		"SELECT COUNT(*) FROM audit_log WHERE action = 'change_password' AND user_id = ?",
		user.ID,
	).Scan(&count); err != nil || count != 1 {
		t.Fatalf("password audit count %d: %v", count, err)
	}
	assertSelfPasswordLogin(t, f, user.Email)
}

func submitSelfPasswordChange(
	t *testing.T,
	f permissionFixture,
	surface, token, session string,
	otherID int64,
) {
	t.Helper()
	if surface == "API" {
		f.request(
			t,
			"POST",
			"/api/v1/users/me/password",
			passwordChangeJSON,
			token,
			"",
			false,
			204,
		)
	} else {
		form := passwordChangeForm()
		form.Set("user_id", fmt.Sprint(otherID))
		status, headers, body := passwordHTTP(
			t,
			f,
			"/settings/security/password",
			form.Encode(),
			session,
			"member-role-csrf",
		)
		if status != 303 || headers.Get("Location") != "/login" ||
			!strings.Contains(
				headers.Get("Set-Cookie"),
				"Max-Age=0",
			) {
			t.Fatalf(
				"password change: %d %v %s",
				status,
				headers,
				body,
			)
		}
	}
}

func assertSelfPasswordLogin(t *testing.T, f permissionFixture, email string) {
	t.Helper()
	for _, login := range []struct {
		password string
		status   int
	}{{"testpass", 422}, {"New-password-141", 303}} {
		status, _, body := passwordHTTP(
			t,
			f,
			"/login",
			url.Values{
				"email":    {email},
				"password": {login.password},
			}.Encode(),
			"",
			"",
		)
		if status != login.status {
			t.Fatalf(
				"login status %d want %d: %s",
				status,
				login.status,
				body,
			)
		}
	}
}

func TestSelfPasswordChangeRejectionsE2E(t *testing.T) {
	f := newPermissionFixture(t)
	user := seedAPIUser(t, f.h.repo, "reject@example.test", "viewer")
	_, token := seedAPIToken(t, f.h.repo, user.ID)
	session := passwordFreshSession(t, f, user.ID)
	for _, input := range []string{
		`{}`, `{"current_password":"wrong","new_password":"New-password-141","password_confirmation":"New-password-141"}`,
		`{"current_password":"testpass","new_password":"short","password_confirmation":"short"}`,
		`{"current_password":"testpass","new_password":"New-password-141","password_confirmation":"mismatch"}`,
	} {
		f.request(
			t,
			"POST",
			"/api/v1/users/me/password",
			input,
			token,
			"",
			false,
			422,
		)
	}
	for _, body := range []string{"null", passwordChangeJSON + `{}`, strings.Replace(passwordChangeJSON, "{", `{"user_id":999,`, 1)} {
		f.request(
			t,
			"POST",
			"/api/v1/users/me/password",
			body,
			token,
			"",
			false,
			400,
		)
	}
	f.request(
		t,
		"POST",
		"/api/v1/users/me/password",
		strings.Repeat(" ", (4<<20)+1),
		token,
		"",
		false,
		413,
	)
	assertSelfPasswordFormRejections(t, f, session)
	for _, check := range []struct {
		session, csrf string
		status        int
	}{
		{session, "", 403}, {"", "", 303},
	} {
		status, _, _ := passwordHTTP(
			t,
			f,
			"/settings/security/password",
			passwordChangeForm().Encode(),
			check.session,
			check.csrf,
		)
		if status != check.status {
			t.Fatalf("web safeguards status %d want %d", status, check.status)
		}
	}
	status, _, _ := passwordHTTP(
		t,
		f,
		"/settings/security/password",
		strings.Repeat("x", (64<<10)+1),
		session,
		"member-role-csrf",
	)
	if status != 413 {
		t.Fatalf("oversized form status %d", status)
	}
	status, _, _ = passwordHTTP(
		t,
		f,
		"/api/v1/users/me/password",
		passwordChangeJSON,
		session,
		"member-role-csrf",
	)
	if status != 401 {
		t.Fatalf("API accepted session authentication: %d", status)
	}
	row, restricted := seedAPIToken(t, f.h.repo, user.ID)
	for _, grant := range []repository.TokenScope{
		{Scope: "global", Role: "viewer"}, {Scope: "global", Role: "admin"},
	} {
		if _, err := f.h.repo.ReplaceTokenScope(
			t.Context(),
			row,
			grant,
		); err != nil {
			t.Fatal(err)
		}
		f.request(
			t,
			"POST",
			"/api/v1/users/me/password",
			passwordChangeJSON,
			restricted,
			"",
			false,
			403,
		)
	}
	project := seedProject(t, f.h.repo)
	if _, err := f.h.repo.ReplaceTokenScope(
		t.Context(),
		row,
		repository.TokenScope{
			Scope: "scoped", Role: "viewer", ProjectIDs: []int64{project.ID},
		},
	); err != nil {
		t.Fatal(err)
	}
	f.request(
		t,
		"POST",
		"/api/v1/users/me/password",
		passwordChangeJSON,
		restricted,
		"",
		false,
		403,
	)
	stored, err := f.h.repo.Queries.GetUserByID(t.Context(), user.ID)
	if err != nil || stored.PasswordHash != user.PasswordHash {
		t.Fatal("rejection changed password")
	}
	f.request(t, "GET", "/settings/security", "", "", session, false, 200)
	if err := f.h.repo.Queries.MarkSessionReauthenticated(
		t.Context(),
		db.MarkSessionReauthenticatedParams{ID: session},
	); err != nil {
		t.Fatal(err)
	}
	status, headers, _ := passwordHTTP(
		t,
		f,
		"/settings/security/password",
		passwordChangeForm().Encode(),
		session,
		"member-role-csrf",
	)
	if status != 303 || headers.Get("Location") != "/settings/security/reauth" {
		t.Fatal("stale security session accepted")
	}
	if err := f.h.repo.Queries.UpdateUserPassword(
		t.Context(),
		db.UpdateUserPasswordParams{ID: user.ID},
	); err != nil {
		t.Fatal(err)
	}
	f.request(
		t,
		"POST",
		"/api/v1/users/me/password",
		passwordChangeJSON,
		token,
		"",
		false,
		422,
	)
	page := string(
		f.request(t, "GET", "/settings/security", "", "", session, false, 200),
	)
	if !strings.Contains(page, "SSO identity provider") ||
		strings.Contains(page, `name="current_password"`) {
		t.Fatal("SSO-only guidance missing")
	}
}

func assertSelfPasswordFormRejections(
	t *testing.T, f permissionFixture, session string,
) {
	t.Helper()
	for _, field := range []string{"current_password", "new_password", "password_confirmation"} {
		form := passwordChangeForm()
		form.Set(field, "wrong")
		page := string(
			f.request(
				t,
				"POST",
				"/settings/security/password",
				form.Encode(),
				"",
				session,
				false,
				422,
			),
		)
		if !strings.Contains(page, `id="password-error"`) ||
			strings.Contains(page, `value="wrong"`) ||
			strings.Contains(page, "New-password-141") {
			t.Fatal("inline rejection lost its error or exposed a password")
		}
	}
}

func TestSelfPasswordChangeBrowserInputE2E(t *testing.T) {
	f := permissionFixture{h: newAPIHarness(t)}
	authHandler := handler.NewAuthHandler(f.h.repo)
	authHandler.SetMFAService(mfa.NewService(mfa.Config{}, nil))
	f.server = httptest.NewServer(server.NewRouter(f.h.repo, f.h.runner,
		cron.NewParser(cron.Minute|cron.Hour|cron.Month|cron.Dom|cron.Dow),
		authHandler))
	t.Cleanup(f.server.Close)
	user := seedAPIUser(t, f.h.repo, "boundary@example.test", "viewer")
	_, token := seedAPIToken(t, f.h.repo, user.ID)
	session := passwordFreshSession(t, f, user.ID)
	challenges := mfa.NewChallengeService(mfa.ChallengeServiceConfig{
		Repository: f.h.repo,
	})
	challenge, err := challenges.Issue(t.Context(), mfa.ChallengeIssue{
		UserID: user.ID, SessionID: session,
		Purpose: mfa.ChallengePurposeTOTPVerify,
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, password := range map[string]string{
		"ASCII":   strings.Repeat("a", 80000),
		"percent": strings.Repeat("%", 24000),
		"tilde":   strings.Repeat("~", 22000),
		"Unicode": strings.Repeat("é", 12000),
		"CR":      "New-password-\r141",
		"LF":      "New-password-\n141",
	} {
		t.Run(name, func(t *testing.T) {
			body, err := json.Marshal(handler.PasswordChangeRequest{
				CurrentPassword: "testpass", NewPassword: password,
				Confirmation: password,
			})
			if err != nil {
				t.Fatal(err)
			}
			f.request(t, "POST", "/api/v1/users/me/password",
				string(body), token, "", false, 422)
		})
	}
	form := passwordChangeForm()
	form.Set("new_password", "New-password-\n141")
	form.Set("password_confirmation", "New-password-\n141")
	status, _, page := passwordHTTP(t, f, "/settings/security/password",
		form.Encode(), session, "member-role-csrf")
	if status != 422 || !strings.Contains(page, `id="password-error"`) {
		t.Fatalf("browser input rejection status %d", status)
	}
	stored, err := f.h.repo.Queries.GetUserByID(t.Context(), user.ID)
	if err != nil || stored.PasswordHash != user.PasswordHash {
		t.Fatal("rejection changed password")
	}
	f.request(t, "GET", "/settings/security", "", "", session, false, 200)
	if _, err := challenges.ResolveBound(t.Context(), mfa.ChallengeBinding{
		Token: challenge.Token, CSRF: challenge.CSRF,
		UserID: user.ID, SessionID: session,
		Purpose: mfa.ChallengePurposeTOTPVerify,
	},
	); err != nil {
		t.Fatalf("rejection removed challenge: %v", err)
	}
	var count int
	if err := f.h.repo.DB.QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM audit_log WHERE action = 'change_password' AND user_id = ?",
		user.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rejection audit count %d: %v", count, err)
	}
	assertSelfPasswordBoundaryLogin(t, f, user.Email, token)
}

func assertSelfPasswordBoundaryLogin(
	t *testing.T, f permissionFixture, email, token string,
) {
	t.Helper()
	form := url.Values{
		"email": {email}, "current_password": {""},
		"new_password": {""}, "password_confirmation": {""},
		"csrf_token": {strings.Repeat("0", 32)},
	}
	available := (int(handler.MaxPasswordFormBodyBytes) - len(form.Encode())) / 3
	password := strings.Repeat("~", available/3) +
		strings.Repeat("a", available%3)
	body, err := json.Marshal(handler.PasswordChangeRequest{
		CurrentPassword: "testpass", NewPassword: password,
		Confirmation: password,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.request(t, "POST", "/api/v1/users/me/password", string(body),
		token, "", false, 204)
	form = url.Values{
		"email": {email}, "password": {password}, "csrf_token": {""},
	}
	status, headers, _ := passwordHTTP(t, f, "/login",
		strings.ReplaceAll(form.Encode(), "~", "%7E"), "", "")
	if status != 303 {
		t.Fatalf("accepted boundary password login status %d", status)
	}
	var session string
	for _, cookie := range (&http.Response{Header: headers}).Cookies() {
		if cookie.Name == "session" {
			session = cookie.Value
		}
	}
	stored, err := f.h.repo.Queries.GetSession(t.Context(),
		db.GetSessionParams{ID: session})
	if err != nil {
		t.Fatal(err)
	}
	form.Del("email")
	form.Set("csrf_token", stored.CsrfToken)
	encoded := strings.ReplaceAll(form.Encode(), "~", "%7E")
	status, _, _ = passwordHTTP(t, f, "/settings/security/reauth",
		encoded, session,
		stored.CsrfToken)
	if status != 303 {
		t.Fatalf(
			"accepted boundary password reauthentication status %d",
			status,
		)
	}
	replacement := strings.ReplaceAll(password, "~", "%")
	form = url.Values{
		"csrf_token": {stored.CsrfToken}, "current_password": {password},
		"new_password": {replacement}, "password_confirmation": {replacement},
	}
	status, _, _ = passwordHTTP(t, f, "/settings/security/password",
		strings.ReplaceAll(form.Encode(), "~", "%7E"), session,
		stored.CsrfToken)
	if status != 303 {
		t.Fatalf("accepted boundary password rotation status %d", status)
	}
	form = url.Values{
		"email": {email}, "password": {replacement}, "csrf_token": {""},
	}
	status, _, _ = passwordHTTP(t, f, "/login", form.Encode(), "", "")
	if status != 303 {
		t.Fatalf("rotated boundary password login status %d", status)
	}
}
