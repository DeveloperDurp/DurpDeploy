//go:build e2e

package api_test

import (
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
)

func TestOpenLogStreamRevocationAPIWebE2E(t *testing.T) {
	f := newArtifactE2E(t)
	release := seedRelease(t, f.h.repo, f.project.ID)
	deployment := seedDeployment(t, f.h.repo, release.ID,
		f.environment.ID, "running")
	if _, err := f.h.repo.Queries.CreateDeploymentLog(
		t.Context(),
		db.CreateDeploymentLogParams{
			DeploymentID: deployment.ID,
			Line:         "before-revocation",
		},
	); err != nil {
		t.Fatal(err)
	}
	for _, surface := range []string{"api", "web"} {
		for _, format := range []string{"sse", "structured", "ndjson"} {
			t.Run(surface+"/"+format, func(t *testing.T) {
				// Given: a project member has an already-open authenticated stream.
				user := seedAPIUser(t, f.h.repo,
					surface+"-"+format+"@revocation.example.test", "viewer")
				f.api(t, "POST", f.base()+"/members", map[string]any{
					"user_id": user.ID, "role": "deployer",
				}, 201)
				id, token := seedAPIToken(t, f.h.repo, user.ID)
				session, csrf, err := auth.NewSessionToken()
				if err != nil {
					t.Fatal(err)
				}
				if _, err := f.h.repo.Queries.CreateSession(t.Context(),
					db.CreateSessionParams{
						ID:        session,
						UserID:    user.ID,
						CsrfToken: csrf,
						ExpiresAt: time.Now().Add(time.Hour).Unix(),
					}); err != nil {
					t.Fatal(err)
				}
				prefix := ""
				if surface == "api" {
					prefix = "/api/v1"
				}
				path := fmt.Sprintf("%s/deployments/%d/logs/stream?format=%s",
					prefix, deployment.ID, format)
				request, err := http.NewRequestWithContext(t.Context(),
					"GET", f.baseURL+path, nil)
				if err != nil {
					t.Fatal(err)
				}
				if surface == "api" {
					request.Header.Set("Authorization", "Bearer "+token)
				} else {
					request.AddCookie(
						&http.Cookie{Name: "session", Value: session},
					)
				}
				response, err := f.client.Do(request)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				if response.StatusCode != 200 {
					t.Fatalf("stream status=%d", response.StatusCode)
				}
				closed := make(chan error, 1)
				go func() {
					_, err := io.Copy(io.Discard, response.Body)
					closed <- err
				}()
				// When: an administrator revokes its durable credential via the API.
				if surface == "api" {
					f.api(t, "DELETE", "/api/v1/admin/tokens/"+id, nil, 204)
				} else {
					f.api(
						t,
						"PUT",
						fmt.Sprintf("/api/v1/admin/users/%d", user.ID),
						map[string]string{
							"email":    user.Email,
							"name":     user.Name,
							"role":     user.Role,
							"password": "Replacement-fixture-123!",
						},
						200,
					)
				}
				// Then: the existing connection ends within the documented grace period.
				select {
				case err := <-closed:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("revoked stream remained open")
				}
			})
		}
	}
}
