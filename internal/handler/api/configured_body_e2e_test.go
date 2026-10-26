//go:build e2e

package api_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/config"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/mfa"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/secret"
	"durpdeploy/internal/server"
	"github.com/robfig/cron/v3"
)

func newConfiguredBodyFixture(
	t *testing.T,
	limits config.HTTPLimits,
) *artifactE2E {
	t.Helper()
	settings := config.Defaults()
	settings.Container.Enabled = "false"
	settings.Limits.HTTP = limits
	if err := settings.Limits.Validate(); err != nil {
		t.Fatal(err)
	}
	h := newAPIHarness(t)
	h.repo = repository.NewConfigured(h.repo.DB, settings)
	h.runner = runner.New(h.repo, h.broker)
	t.Cleanup(h.runner.KillAll)
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	h.repo.SetSecretBox(box)
	user := seedAPIUser(t, h.repo, "configured-body@test.local", "admin")
	_, token := seedAPIToken(t, h.repo, user.ID)
	authHandler := handler.NewAuthHandler(h.repo)
	authHandler.SetMFAService(mfa.NewService(mfa.Config{}, box))
	srv := httptest.NewServer(server.NewRouter(h.repo, h.runner,
		cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
		authHandler))
	t.Cleanup(srv.Close)
	return &artifactE2E{
		h: h, server: srv, baseURL: srv.URL, token: token,
		session: memberRoleSession(t, h, user.ID), csrf: "member-role-csrf",
		project: seedProject(t, h.repo),
		client: &http.Client{Timeout: 15 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			}},
	}
}

type configuredBodyCase struct {
	method, path, body, contentType string
	limit                           int64
	status, oversized               int
	api                             bool
}

func TestConfiguredRequestBodyAPIWebE2E(t *testing.T) {
	for _, raised := range []bool{false, true} {
		t.Run(fmt.Sprintf("raised=%t", raised), func(t *testing.T) {
			limits := config.Defaults().Limits.HTTP
			limits.JSONBytes, limits.FormBytes = 1024, 2048
			limits.PasswordFormBytes, limits.RepositoryFormBytes = 512, 768
			if raised {
				limits = config.Defaults().Limits.HTTP
				limits.JSONBytes += 1024
				limits.FormBytes += 1024
				limits.PasswordFormBytes += 1024
				limits.RepositoryFormBytes += 1024
			}
			f := newConfiguredBodyFixture(t, limits)
			for _, tc := range []configuredBodyCase{
				{"POST", "/api/v1/projects", `{"name":"body-api"}`, "application/json", limits.JSONBytes, 201, 413, true},
				{"GET", "/api/v1/projects", `{}`, "application/json", limits.JSONBytes, 200, 413, true},
				{"GET", "/api/v1/healthz", `{}`, "application/json", limits.JSONBytes, 200, 413, true},
				{"POST", "/api/lint", `{"script":"echo ok"}`, "application/json", limits.JSONBytes, 200, 413, false},
				{"POST", "/projects", "name=body-web&padding=", "application/x-www-form-urlencoded", limits.FormBytes, 303, 413, false},
				{"POST", "/projects", "--body-boundary\r\nContent-Disposition: form-data; name=\"name\"\r\n\r\nbody-multipart\r\n--body-boundary--\r\n", "multipart/form-data; boundary=body-boundary", limits.FormBytes, 303, 413, false},
				{"POST", fmt.Sprintf("/projects/%d/package-repository", f.project.ID), "url_template=https%3A%2F%2Fpackages.example.test%2F%7Bversion%7D.zip&auth_type=noauth&padding=", "application/x-www-form-urlencoded", limits.RepositoryFormBytes, 303, 413, false},
				{"POST", "/login", "email=configured-body%40test.local&password=testpass&padding=", "application/x-www-form-urlencoded", limits.PasswordFormBytes, 303, 400, false},
				{"POST", "/settings/security/reauth", "password=testpass&padding=", "application/x-www-form-urlencoded", limits.PasswordFormBytes, 303, 422, false},
				{"POST", "/settings/security/password", "current_password=testpass&padding=", "application/x-www-form-urlencoded", limits.PasswordFormBytes, 422, 413, false},
			} {
				t.Run(
					tc.path,
					func(t *testing.T) { checkConfiguredBodyBoundary(t, f, tc) },
				)
			}
		})
	}
}

func checkConfiguredBodyBoundary(
	t *testing.T,
	f *artifactE2E,
	tc configuredBodyCase,
) {
	t.Helper()
	for _, unknown := range []bool{false, true} {
		for _, extra := range []int64{0, 1} {
			checkConfiguredBodyRequest(t, f, tc, unknown, extra)
		}
	}
}

func checkConfiguredBodyRequest(
	t *testing.T,
	f *artifactE2E,
	tc configuredBodyCase,
	unknown bool,
	extra int64,
) {
	t.Helper()
	input := strings.ReplaceAll(
		tc.body,
		"body-api",
		fmt.Sprintf("body-api-%t", unknown),
	)
	input = strings.ReplaceAll(
		input,
		"body-web",
		fmt.Sprintf("body-web-%t", unknown),
	)
	input = strings.ReplaceAll(
		input,
		"body-multipart",
		fmt.Sprintf("body-multipart-%t", unknown),
	)
	body := input + strings.Repeat(" ", int(tc.limit+extra)-len(input))
	request, err := http.NewRequestWithContext(
		t.Context(),
		tc.method,
		f.baseURL+tc.path,
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", tc.contentType)
	if tc.api {
		request.Header.Set("Authorization", "Bearer "+f.token)
	} else {
		request.AddCookie(&http.Cookie{Name: "session", Value: f.session})
		request.Header.Set("X-CSRF-Token", f.csrf)
	}
	if unknown {
		request.ContentLength = -1
	}
	before, beforeAudit := proxyBodyState(t, f)
	response, err := f.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	want := tc.status
	if extra != 0 {
		want = tc.oversized
		after, afterAudit := proxyBodyState(t, f)
		if before != after || beforeAudit != afterAudit {
			t.Fatal(
				"oversized request changed projects or successful audit history",
			)
		}
	}
	if response.StatusCode != want || readErr != nil ||
		closeErr != nil {
		t.Fatalf(
			"unknown=%t extra=%d status=%d want=%d body=%.200s read=%v close=%v",
			unknown,
			extra,
			response.StatusCode,
			want,
			data,
			readErr,
			closeErr,
		)
	}
}

func TestConfiguredPasswordCompatibilityE2E(t *testing.T) {
	for _, raised := range []bool{false, true} {
		t.Run(fmt.Sprintf("raised=%t", raised), func(t *testing.T) {
			limits := config.Defaults().Limits.HTTP
			limits.PasswordFormBytes = 512
			password := strings.Repeat("p", 200)
			status := 422
			if raised {
				limits.PasswordFormBytes = 128 << 10
				password = strings.Repeat("p", 24<<10)
				status = 204
			}
			f := newConfiguredBodyFixture(t, limits)
			f.api(t, "POST", "/api/v1/users/me/password", map[string]string{
				"current_password": "testpass", "new_password": password,
				"password_confirmation": password,
			}, status)
			if raised {
				f.web(t, "POST", "/login", url.Values{
					"email": {
						"configured-body@test.local",
					}, "password": {password},
				}, 303)
			}
		})
	}
}

func TestConfiguredRequestBodyIsolationE2E(t *testing.T) {
	limits := config.Defaults().Limits.HTTP
	limits.JSONBytes = 1024
	first := newConfiguredBodyFixture(t, limits)
	limits.JSONBytes = 2048
	second := newConfiguredBodyFixture(t, limits)
	for _, f := range []*artifactE2E{first, second, first, second} {
		want := 201
		if f == first {
			want = 413
		}
		f.api(t, "POST", "/api/v1/projects", map[string]string{
			"name":        fmt.Sprintf("independent-%d", time.Now().UnixNano()),
			"description": strings.Repeat("x", 1500),
		}, want)
	}
}
