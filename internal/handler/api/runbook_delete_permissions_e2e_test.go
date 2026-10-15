//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
)

type deletionRunbook struct {
	Runbook db.Runbook        `json:"runbook"`
	Version db.RunbookVersion `json:"version"`
}

func createDeletionRunbook(
	t *testing.T,
	f *artifactE2E,
	name string,
) deletionRunbook {
	t.Helper()
	var saved deletionRunbook
	body := f.api(t, "POST", f.base()+"/runbooks", map[string]any{
		"name": name, "steps": []map[string]string{{"name": "check",
			"script_body": "printf deletion-check", "container_image": "docker.io/library/bash:5.2"}},
	}, http.StatusCreated)
	if err := json.Unmarshal(body, &saved); err != nil {
		t.Fatal(err)
	}
	return saved
}

func TestRunbookDeletionPermissionsAPIWebE2E(t *testing.T) {
	// Given: a saved runbook and member/nonmember identities.
	f := newArtifactE2E(t)
	book := createDeletionRunbook(t, f, "permissions").Runbook
	path := fmt.Sprintf("%s/runbooks/%d", f.base(), book.ID)
	webPath := strings.TrimPrefix(path, "/api/v1")
	var writerID int64
	var writerToken string
	for _, role := range []string{"viewer", "deployer", "maintainer"} {
		member, userID := newDeletionMember(t, f, role)
		// When: unauthorized users attempt deletion at each HTTP boundary.
		if role != "maintainer" {
			if err := f.h.repo.Queries.AddProjectMember(t.Context(), db.AddProjectMemberParams{
				ProjectID: f.project.ID, UserID: userID, Role: "deployer",
			}); err != nil {
				t.Fatal(err)
			}
			member.api(t, "DELETE", path, nil, http.StatusForbidden)
			member.web(t, "POST", webPath+"/delete", nil, http.StatusForbidden)
			page := member.web(t, "GET", webPath, nil, http.StatusOK)
			if strings.Contains(page, webPath+"/delete") {
				t.Fatal("viewer sees deletion action")
			}
		} else {
			writerID, writerToken = userID, member.token
			member.api(t, "DELETE", path, nil, http.StatusNotFound)
			member.web(t, "POST", webPath+"/delete", nil, http.StatusNotFound)
		}
	}
	unauthenticated := *f
	unauthenticated.token = ""
	unauthenticated.api(t, "DELETE", path, nil, http.StatusUnauthorized)
	f.api(
		t,
		"DELETE",
		path,
		map[string]string{"name": "forged"},
		http.StatusBadRequest,
	)
	assertDeletionMissingCSRF(t, f, webPath)
	// Then: no unauthorized operation changes the runbook or records deletion.
	f.api(t, "GET", path, nil, http.StatusOK)
	var count int
	if err := f.h.repo.DB.QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM audit_log WHERE action='delete_runbook'",
	).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rejected requests recorded deletion=%d error=%v", count, err)
	}
	if err := f.h.repo.Queries.AddProjectMember(t.Context(), db.AddProjectMemberParams{
		ProjectID: f.project.ID, UserID: writerID, Role: "maintainer",
	}); err != nil {
		t.Fatal(err)
	}
	writer := *f
	writer.token = writerToken
	writer.api(t, "DELETE", path, nil, http.StatusNoContent)
}

func newDeletionMember(
	t *testing.T, f *artifactE2E, role string,
) (artifactE2E, int64) {
	t.Helper()
	user := seedAPIUser(t, f.h.repo, role+"-delete@example.test", role)
	_, token := seedAPIToken(t, f.h.repo, user.ID)
	session, csrf, err := auth.NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.repo.Queries.CreateSession(t.Context(), db.CreateSessionParams{
		ID: session, UserID: user.ID, CsrfToken: csrf,
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	member := *f
	member.token, member.session, member.csrf = token, session, csrf
	return member, user.ID
}

func assertDeletionMissingCSRF(
	t *testing.T, f *artifactE2E, webPath string,
) {
	t.Helper()
	request, err := http.NewRequestWithContext(
		t.Context(), "POST", f.baseURL+webPath+"/delete", nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(&http.Cookie{Name: "session", Value: f.session})
	response, err := f.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("missing CSRF status=%d", response.StatusCode)
	}
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		t.Fatal(err)
	}
}
