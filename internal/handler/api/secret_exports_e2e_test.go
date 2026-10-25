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
	"sync"
	"testing"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/mfa"
	"durpdeploy/internal/oidc"
	"durpdeploy/internal/oidc/oidctest"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/secret"
	"durpdeploy/internal/server"
	"github.com/robfig/cron/v3"
)

type exportFixture struct {
	h                             *harness
	user                          *db.User
	server                        *httptest.Server
	session, csrf, token, tokenID string
	project                       db.Project
	lifecycle                     db.Lifecycle
	issuer                        *oidctest.Fixture
}

type exportRequest struct {
	ID          string `json:"id"`
	Claim       string `json:"claim_token"`
	ApprovalURL string `json:"approval_url"`
}

func newExportFixture(t *testing.T, withOIDC bool) *exportFixture {
	t.Helper()
	f := &exportFixture{h: newAPIHarness(t)}
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	f.h.repo.SetSecretBox(box)
	f.user = seedAPIUser(t, f.h.repo, "export@example.test", "admin")
	f.tokenID, f.token = seedAPIToken(t, f.h.repo, f.user.ID)
	f.session, f.csrf, err = auth.NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.h.repo.Queries.CreateSession(t.Context(), db.CreateSessionParams{
		ID: f.session, UserID: f.user.ID, CsrfToken: f.csrf, ExpiresAt: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	f.project = seedProject(t, f.h.repo)
	f.lifecycle, err = f.h.repo.Queries.CreateLifecycle(
		t.Context(),
		db.CreateLifecycleParams{Name: "Export lifecycle"},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, marked := range []int64{0, 1} {
		_, err = f.h.repo.CreateVariable(t.Context(), db.CreateVariableParams{
			ProjectID: f.project.ID, Name: fmt.Sprintf("PROJECT_%d", marked),
			Value: sql.NullString{
				String: "harmless-project-fixture",
				Valid:  true,
			}, Secret: marked,
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.h.repo.SaveLifecycleVariable(
			t.Context(),
			repository.LifecycleVariableInput{
				CreateLifecycleVariableParams: db.CreateLifecycleVariableParams{
					LifecycleID: f.lifecycle.ID, Name: fmt.Sprintf("SHARED_%d", marked),
					Value: sql.NullString{
						String: "harmless-lifecycle-fixture",
						Valid:  true,
					}, Secret: marked,
				},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
	}
	f.server = httptest.NewUnstartedServer(nil)
	base := "http://" + f.server.Listener.Addr().String()
	authHandler := handler.NewAuthHandler(f.h.repo)
	authHandler.SetMFAService(mfa.NewService(mfa.Config{}, box))
	if withOIDC {
		if err := f.h.repo.Queries.UpdateUserPassword(
			t.Context(), db.UpdateUserPasswordParams{ID: f.user.ID},
		); err != nil {
			t.Fatal(err)
		}
		f.issuer, err = oidctest.New(
			oidctest.Options{
				Now:         time.Now(),
				CallbackURL: base + "/login/oidc/callback",
				Claims: oidctest.Claims{
					Subject: "export-subject", Email: f.user.Email, EmailVerified: true, AuthTime: time.Now(), Groups: []string{"fixture-admin"},
				},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(f.issuer.Close)
		codec, err := oidc.NewTransactionCookieCodec(
			box,
			oidc.TransactionCookieConfig{Now: f.issuer.Now},
		)
		if err != nil {
			t.Fatal(err)
		}
		store, err := oidc.NewTransactionStore(
			oidc.TransactionStoreOptions{
				Repository:  f.h.repo,
				CookieCodec: codec,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		provider, err := oidc.NewProvider(oidc.ProviderOptions{
			Config: oidc.Config{
				Enabled:      true,
				Issuer:       f.issuer.URL(),
				ClientID:     f.issuer.ClientID(),
				ClientSecret: f.issuer.ClientSecret(),
				CallbackURL:  base + "/login/oidc/callback",
				Scopes:       []string{"openid"},
				GroupClaim:   "groups",
				AdminGroup:   "fixture-admin",
			},
			HTTPClient: f.issuer.Client(), Now: f.issuer.Now,
		})
		if err != nil {
			t.Fatal(err)
		}
		authHandler.SetOIDCLogin(provider, store)
		_, err = f.h.repo.Queries.CreateOIDCIdentity(
			t.Context(),
			db.CreateOIDCIdentityParams{
				Issuer: f.issuer.URL(), Subject: "export-subject", UserID: f.user.ID,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
	}
	f.server.Config.Handler = server.NewRouter(
		f.h.repo,
		f.h.runner,
		cron.NewParser(
			cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow,
		),
		authHandler,
		withOIDC,
	)
	f.server.Start()
	t.Cleanup(f.server.Close)
	return f
}

func (f *exportFixture) request(
	t *testing.T,
	method, path, body string,
	api bool,
) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(
		t.Context(),
		method,
		f.server.URL+path,
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	if api {
		req.Header.Set("Authorization", "Bearer "+f.token)
		req.Header.Set("Content-Type", "application/json")
	} else {
		req.AddCookie(&http.Cookie{Name: "session", Value: f.session})
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-CSRF-Token", f.csrf)
	}
	client := &http.Client{
		Timeout:       15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
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
	return response.StatusCode, response.Header, data
}

func (f *exportFixture) start(
	t *testing.T,
	scope string,
	api bool,
) exportRequest {
	t.Helper()
	id := f.project.ID
	if scope == "lifecycle" {
		id = f.lifecycle.ID
	}
	body := fmt.Sprintf(`{"scope":%q,"scope_id":%d}`, scope, id)
	path := "/api/v1/admin/secret-exports"
	if !api {
		body = url.Values{
			"scope":    {scope},
			"scope_id": {fmt.Sprint(id)},
		}.Encode()
		path = "/admin/secret-exports"
	}
	status, headers, data := f.request(t, "POST", path, body, api)
	var result exportRequest
	if api {
		if status != 201 {
			t.Fatalf("create API export = %d: %s", status, data)
		}
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatal(err)
		}
		if result.Claim == "" ||
			result.ApprovalURL != "/admin/secret-exports/"+result.ID {
			t.Fatal("missing API approval contract")
		}
	} else {
		if status != 303 {
			t.Fatalf("create web export = %d", status)
		}
		result.ID = strings.TrimPrefix(headers.Get("Location"), "/admin/secret-exports/")
	}
	if headers.Get("Cache-Control") != "no-store" {
		t.Fatal("export response is cacheable")
	}
	return result
}

func (f *exportFixture) authorize(t *testing.T, row exportRequest) {
	t.Helper()
	status, _, _ := f.request(
		t,
		"POST",
		"/admin/secret-exports/"+row.ID+"/authorize",
		"",
		false,
	)
	if status != 303 {
		t.Fatalf("authorize export = %d", status)
	}
}

func (f *exportFixture) password(
	t *testing.T,
	row exportRequest,
	password string,
) int {
	t.Helper()
	status, _, _ := f.request(
		t,
		"POST",
		"/settings/security/reauth",
		url.Values{"password": {password}, "export_id": {row.ID}}.Encode(),
		false,
	)
	return status
}

func (f *exportFixture) download(
	t *testing.T,
	row exportRequest,
	api bool,
) (int, http.Header, []byte) {
	t.Helper()
	path := "/admin/secret-exports/" + row.ID + "/download"
	if api {
		path = "/api/v1" + path
	}
	body := ""
	if api {
		body = fmt.Sprintf(`{"claim_token":%q}`, row.Claim)
	}
	return f.request(t, "POST", path, body, api)
}

func (f *exportFixture) oidc(t *testing.T, row exportRequest) int {
	t.Helper()
	status, headers, _ := f.request(
		t,
		"GET",
		"/settings/security/reauth/oidc?export_id="+row.ID,
		"",
		false,
	)
	if status != 303 {
		t.Fatalf("OIDC start = %d", status)
	}
	response := &http.Response{Header: headers}
	client := *f.issuer.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	authorize, err := client.Get(headers.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	defer authorize.Body.Close()
	if authorize.StatusCode != 302 {
		t.Fatalf("OIDC authorization = %d", authorize.StatusCode)
	}
	req, err := http.NewRequestWithContext(
		t.Context(),
		"GET",
		authorize.Header.Get("Location"),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, cookie := range response.Cookies() {
		req.AddCookie(cookie)
	}
	req.AddCookie(&http.Cookie{Name: "session", Value: f.session})
	callback, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer callback.Body.Close()
	if f.issuer.Capture().Authorization.Prompt != "login" ||
		f.issuer.Capture().Authorization.MaxAge != "1" {
		t.Fatal("OIDC did not require fresh authentication")
	}
	if callback.Header.Get("Location") != "/admin/secret-exports/"+row.ID {
		return 422
	}
	return callback.StatusCode
}

func TestSecretExportAPIWebE2E(t *testing.T) {
	for _, api := range []bool{false, true} {
		for _, scope := range []string{"project", "lifecycle"} {
			for _, method := range []string{"password", "oidc"} {
				t.Run(
					fmt.Sprintf("api=%t/%s/%s", api, scope, method),
					func(t *testing.T) {
						testSecretExportAPIWeb(t, api, scope, method)
					},
				)
			}
		}
	}
}

func testSecretExportAPIWeb(t *testing.T, api bool, scope, method string) {
	t.Helper()
	f := newExportFixture(t, method == "oidc")
	if method == "oidc" {
		f.issuer.EmulateAuthentikReauth()
	}
	row := f.start(t, scope, api)
	if status, _, _ := f.download(t, row, api); status != 404 {
		t.Fatalf("unapproved download = %d", status)
	}
	f.authorize(t, row)
	if status := f.password(t, row, "incorrect"); status != 422 {
		t.Fatalf("wrong password = %d", status)
	}
	if status, _, _ := f.download(t, row, api); status != 404 {
		t.Fatal("failed reauthentication allowed export")
	}
	status := 0
	if method == "oidc" {
		if f.password(t, row, "testpass") != 422 {
			t.Fatal("SSO-only account accepted a local password")
		}
		status = f.oidc(t, row)
	} else {
		status = f.password(t, row, "testpass")
	}
	if status != 303 {
		t.Fatalf("reauthentication = %d", status)
	}
	if method == "oidc" && f.issuer.Counters().Reauthenticate != 1 {
		t.Fatal("OIDC export did not require a new provider sign-in")
	}
	f.checkDownload(t, row, api, scope)
}

func (f *exportFixture) checkDownload(
	t *testing.T,
	row exportRequest,
	api bool,
	scope string,
) {
	t.Helper()
	status, headers, data := f.download(t, row, api)
	if status != 200 {
		t.Fatalf("approved download = %d: %s", status, data)
	}
	if headers.Get("Cache-Control") != "no-store" ||
		!strings.Contains(
			headers.Get("Content-Disposition"),
			"attachment;",
		) {
		t.Fatal("unsafe download headers")
	}
	var doc repository.SecretExportDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Scope != scope || len(doc.Variables) != 1 ||
		doc.Variables[0].Value != "harmless-"+scope+"-fixture" {
		t.Fatalf("wrong export content: %s", data)
	}
	if status, _, _ := f.download(t, row, api); status != 404 {
		t.Fatal("export approval was reused")
	}
	var details string
	if err := f.h.repo.DB.QueryRow("SELECT details FROM audit_log WHERE action = 'export_secrets'").Scan(&details); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(details, "harmless-") ||
		strings.Contains(details, row.Claim) &&
			row.Claim != "" {
		t.Fatal("audit leaked secret material")
	}
}

func TestSecretExportAuthorizationE2E(t *testing.T) {
	for _, role := range []string{"viewer", "deployer", "maintainer"} {
		t.Run(role, func(t *testing.T) {
			f := newExportFixture(t, false)
			if _, err := f.h.repo.DB.Exec("UPDATE users SET role = ? WHERE id = ?", role, f.user.ID); err != nil {
				t.Fatal(err)
			}
			if err := f.h.repo.Queries.AddProjectMember(t.Context(), db.AddProjectMemberParams{ProjectID: f.project.ID, UserID: f.user.ID, Role: "admin"}); err != nil {
				t.Fatal(err)
			}
			for _, api := range []bool{false, true} {
				path, body := "/admin/secret-exports", fmt.Sprintf(
					"scope=project&scope_id=%d",
					f.project.ID,
				)
				if api {
					path, body = "/api/v1"+path, fmt.Sprintf(
						`{"scope":"project","scope_id":%d}`,
						f.project.ID,
					)
				}
				if status, _, _ := f.request(t, "POST", path, body, api); status != 403 {
					t.Fatalf(
						"project admin with global %s, api=%t = %d",
						role,
						api,
						status,
					)
				}
			}
			_, _, page := f.request(
				t,
				"GET",
				fmt.Sprintf("/projects/%d/variables", f.project.ID),
				"",
				false,
			)
			if strings.Contains(string(page), "Export secrets") {
				t.Fatal("non-admin sees export control")
			}
		})
	}
}

func TestSecretExportInvalidationE2E(t *testing.T) {
	for _, reason := range []string{"cancel", "expire", "logout", "demote", "claim", "other-token", "other-session", "ordinary-reauth"} {
		t.Run(reason, func(t *testing.T) {
			testSecretExportInvalidation(t, reason)
		})
	}
}

func testSecretExportInvalidation(t *testing.T, reason string) {
	t.Helper()
	f := newExportFixture(t, false)
	row := f.start(t, "project", true)
	f.authorize(t, row)
	if reason == "ordinary-reauth" {
		status, _, _ := f.request(
			t,
			"POST",
			"/settings/security/reauth",
			"password=testpass",
			false,
		)
		if status != 303 {
			t.Fatalf("ordinary reauthentication = %d", status)
		}
	} else if status := f.password(t, row, "testpass"); status != 303 {
		t.Fatalf("approve = %d", status)
	}
	switch reason {
	case "cancel":
		f.request(
			t,
			"POST",
			"/admin/secret-exports/"+row.ID+"/cancel",
			"",
			false,
		)
	case "expire":
		_, err := f.h.repo.DB.Exec(
			"UPDATE secret_exports SET expires_at = 1 WHERE id = ?",
			row.ID,
		)
		if err != nil {
			t.Fatal(err)
		}
	case "logout":
		if err := f.h.repo.Queries.DeleteSessionsByUser(t.Context(), f.user.ID); err != nil {
			t.Fatal(err)
		}
	case "demote":
		if _, err := f.h.repo.DB.Exec("UPDATE users SET role = 'maintainer' WHERE id = ?", f.user.ID); err != nil {
			t.Fatal(err)
		}
	case "claim":
		row.Claim = "wrong-claim"
	case "other-token":
		_, f.token = seedAPIToken(t, f.h.repo, f.user.ID)
	case "other-session":
		f.session, f.csrf, _ = auth.NewSessionToken()
		_, err := f.h.repo.Queries.CreateSession(
			t.Context(),
			db.CreateSessionParams{
				ID:        f.session,
				UserID:    f.user.ID,
				CsrfToken: f.csrf,
				ExpiresAt: time.Now().Add(time.Hour).Unix(),
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if status, _, _ := f.request(t, "GET", "/admin/secret-exports/"+row.ID, "", false); status != 404 {
			t.Fatal("another session viewed approval")
		}
		return
	}
	if status, _, data := f.download(t, row, true); status != 404 &&
		status != 403 {
		t.Fatalf("invalidated export = %d: %s", status, data)
	}
}

func TestSecretExportConcurrentConsumptionE2E(t *testing.T) {
	f := newExportFixture(t, false)
	row := f.start(t, "project", true)
	f.authorize(t, row)
	if f.password(t, row, "testpass") != 303 {
		t.Fatal("approve export")
	}
	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for range 2 {
		wg.Go(
			func() { status, _, _ := f.download(t, row, true); statuses <- status },
		)
	}
	wg.Wait()
	close(statuses)
	success := 0
	for status := range statuses {
		if status == 200 {
			success++
		} else if status != 404 {
			t.Fatalf("competing download = %d", status)
		}
	}
	if success != 1 {
		t.Fatalf("successful downloads = %d", success)
	}
}

func TestSecretExportOIDCFailuresE2E(t *testing.T) {
	for _, reason := range []string{"different-subject", "stale-auth", "bad-signature", "canceled"} {
		t.Run(reason, func(t *testing.T) {
			f := newExportFixture(t, true)
			row := f.start(t, "project", true)
			f.authorize(t, row)
			claims := oidctest.Claims{
				Subject:       "export-subject",
				Email:         f.user.Email,
				EmailVerified: true,
				Groups:        []string{"fixture-admin"},
				AuthTime:      f.issuer.Now(),
			}
			switch reason {
			case "different-subject":
				claims.Subject = "another-subject"
			case "stale-auth":
				claims.AuthTime = time.Unix(1, 0)
			case "bad-signature":
				f.issuer.SetTokenMode(oidctest.TokenBadSignature)
			case "canceled":
				f.request(
					t,
					"POST",
					"/admin/secret-exports/"+row.ID+"/cancel",
					"",
					false,
				)
				if status, _, _ := f.request(t, "GET", "/settings/security/reauth/oidc?export_id="+row.ID, "", false); status != 404 {
					t.Fatalf("canceled OIDC start = %d", status)
				}
			}
			f.issuer.SetClaims(claims)
			if reason != "canceled" && f.oidc(t, row) != 422 {
				t.Fatal("invalid OIDC identity approved export")
			}
			if status, _, _ := f.download(t, row, true); status != 404 {
				t.Fatalf("invalid OIDC export = %d", status)
			}
		})
	}
}

func TestSecretExportRequiresNewConfirmationE2E(t *testing.T) {
	f := newExportFixture(t, false)
	first := f.start(t, "project", false)
	f.authorize(t, first)
	if f.password(t, first, "testpass") != 303 {
		t.Fatal("first confirmation failed")
	}
	if status, _, _ := f.download(t, first, false); status != 200 {
		t.Fatal("first download failed")
	}
	second := f.start(t, "project", false)
	if status, _, _ := f.download(t, second, false); status != 404 {
		t.Fatal("recent identity confirmation authorized another export")
	}
}

func TestSecretExportAPIValidationE2E(t *testing.T) {
	f := newExportFixture(t, false)
	for _, id := range []string{"invalid", "0", "-1", "9223372036854775808"} {
		if status, _, _ := f.request(t, "POST", "/admin/secret-exports", "scope=project&scope_id="+id, false); status != 400 {
			t.Fatalf("invalid web scope_id %s = %d", id, status)
		}
	}
	for _, body := range []string{`{`, `{"scope":"instance","scope_id":1}`, `{"scope":"project","scope_id":0}`, `{"scope":"project","scope_id":-1}`} {
		if status, _, _ := f.request(t, "POST", "/api/v1/admin/secret-exports", body, true); status != 400 {
			t.Fatalf("invalid export input = %d", status)
		}
	}
	if status, _, _ := f.request(t, "POST", "/api/v1/admin/secret-exports", `{"scope":"project","scope_id":99999}`, true); status != 404 {
		t.Fatalf("missing scope = %d", status)
	}
	row := f.start(t, "project", true)
	f.authorize(t, row)
	if f.password(t, row, "testpass") != 303 {
		t.Fatal("approve export")
	}
	wrong := row
	wrong.Claim = ""
	if status, _, _ := f.download(t, wrong, true); status != 404 {
		t.Fatal("bearer token alone authorized download")
	}
	if status, _, _ := f.download(t, row, false); status != 404 {
		t.Fatal("browser bypassed API claim requirement")
	}
	other := seedAPIUser(t, f.h.repo, "other-admin@example.test", "admin")
	_, otherToken := seedAPIToken(t, f.h.repo, other.ID)
	original := f.token
	f.token = otherToken
	if status, _, _ := f.download(t, row, true); status != 404 {
		t.Fatal("another admin claimed export")
	}
	f.token = original
	if status, _, _ := f.download(t, row, true); status != 200 {
		t.Fatal("invalid attempts consumed valid approval")
	}
}

func TestSecretExportCSRFProtectionE2E(t *testing.T) {
	f := newExportFixture(t, false)
	row := f.start(t, "project", false)
	f.authorize(t, row)
	if f.password(t, row, "testpass") != 303 {
		t.Fatal("approve export")
	}
	csrf := f.csrf
	f.csrf = ""
	if status, _, _ := f.download(t, row, false); status != 403 {
		t.Fatalf("missing CSRF = %d", status)
	}
	f.csrf = csrf
	if status, _, _ := f.download(t, row, false); status != 200 {
		t.Fatal("CSRF rejection consumed approval")
	}
}
