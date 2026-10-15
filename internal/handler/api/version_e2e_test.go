package api_test

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/buildinfo"
	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/server"
	"github.com/robfig/cron/v3"
)

func TestBuildInfoAPIWebE2E(t *testing.T) {
	h := newAPIHarness(t)
	srv := httptest.NewServer(server.NewRouter(h.repo, h.runner,
		cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
		handler.NewAuthHandler(h.repo)))
	t.Cleanup(srv.Close)
	client := &http.Client{CheckRedirect: func(
		*http.Request, []*http.Request,
	) error {
		return http.ErrUseLastResponse
	}}
	request := func(path, token, session string, want int) []byte {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(),
			"GET", srv.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if session != "" {
			req.AddCookie(&http.Cookie{Name: "session", Value: session})
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != want {
			t.Fatalf("%s: status=%d body=%s error=%v",
				path, response.StatusCode, body, err)
		}
		return body
	}
	request("/api/v1/version", "", "", 401)
	request("/settings/version", "", "", 303)
	for _, role := range []string{"admin", "deployer", "viewer"} {
		t.Run(role, func(t *testing.T) {
			user := seedAPIUser(t, h.repo, role+"@version.test", role)
			_, token := seedAPIToken(t, h.repo, user.ID)
			session := fmt.Sprint(user.ID)
			if _, err := h.repo.Queries.CreateSession(t.Context(),
				db.CreateSessionParams{ID: session, UserID: user.ID,
					CsrfToken: "version-csrf",
					ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
				t.Fatal(err)
			}
			var got buildinfo.Info
			if err := json.Unmarshal(request("/api/v1/version",
				token, "", 200), &got); err != nil {
				t.Fatal(err)
			}
			if want := buildinfo.Current(); got != want {
				t.Fatalf("API build = %+v, want %+v", got, want)
			}
			body := string(request("/settings/version", "", session, 200))
			for _, value := range []string{
				"DurpDeploy version", got.Version, got.Commit,
				got.BuildNumber, `href="/settings/version"`,
			} {
				if !strings.Contains(body, html.EscapeString(value)) &&
					!strings.Contains(body, value) {
					t.Fatalf("web version omitted %q", value)
				}
			}
			request("/api/v1/version", "", session, 401)
		})
	}
}
