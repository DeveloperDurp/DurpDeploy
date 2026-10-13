//go:build e2e && backuprestore && linux

package api_test

import (
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"github.com/google/uuid"
)

func (f *restoreFixture) seedAuth(
	t *testing.T,
	repo *repository.Repository,
) string {
	t.Helper()
	f.password = uuid.NewString()
	hash, err := auth.HashPassword(f.password)
	if err != nil {
		t.Fatal(err)
	}
	user, err := repo.Queries.CreateUser(t.Context(), db.CreateUserParams{
		Email: "restore@example.test", PasswordHash: hash,
		Name: "Recovery admin", Role: "admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, token := seedAPIToken(t, repo, user.ID)
	return token
}

func (f *restoreFixture) login(t *testing.T) {
	t.Helper()
	f.api.web(t, "POST", "/login", url.Values{
		"email": {"restore@example.test"}, "password": {"testpass"},
	}, http.StatusUnprocessableEntity)
	form := url.Values{
		"email":    {"restore@example.test"},
		"password": {f.password},
	}
	req, err := http.NewRequestWithContext(t.Context(), "POST",
		f.api.baseURL+"/login", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := f.api.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("restored password login returned %d", response.StatusCode)
	}
	f.api.session = ""
	for _, cookie := range response.Cookies() {
		if cookie.Name == "session" {
			f.api.session = cookie.Value
		}
	}
	if f.api.session == "" {
		t.Fatal("password login did not issue a session")
	}
	page := f.api.web(t, "GET", "/settings/tokens", nil, 200)
	match := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).
		FindStringSubmatch(page)
	if len(match) != 2 {
		t.Fatal("authenticated page did not render its CSRF token")
	}
	f.api.csrf = match[1]
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		t.Fatal(err)
	}
}
