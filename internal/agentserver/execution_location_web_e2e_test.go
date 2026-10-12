//go:build e2e && agentstaging

package agentserver_test

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
)

func (s *stagingE2E) assertExecutionLocationWeb(t *testing.T, id int64) {
	t.Helper()
	user, err := s.f.repo.Queries.GetUserByEmail(
		t.Context(),
		"staging@test.local",
	)
	if err != nil {
		t.Fatal(err)
	}
	session, csrf, err := auth.NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.f.repo.Queries.CreateSession(t.Context(), db.CreateSessionParams{
		ID: session, UserID: user.ID, CsrfToken: csrf,
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(t.Context(), "GET",
		fmt.Sprintf("%s/deployments/%d", s.app.URL, id), nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(&http.Cookie{Name: "session", Value: session})
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("web logs status=%d error=%v", response.StatusCode, err)
	}
	if strings.Count(string(data), "Execution location:") != 6 ||
		strings.Count(string(data), "Execution agent:") != 4 {
		t.Fatalf("saved locations missing from step panels: %s", data)
	}
}
