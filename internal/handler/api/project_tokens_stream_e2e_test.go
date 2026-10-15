//go:build e2e

package api_test

import (
	"durpdeploy/internal/db"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestProjectTokensStreamReauthorizationAPIE2E(t *testing.T) {
	for _, format := range []string{"sse", "ndjson", "structured"} {
		for _, change := range []string{"scope", "revoke", "expire"} {
			t.Run(format+"/"+change, func(t *testing.T) {
				f := newArtifactE2E(t)
				other := seedProject(t, f.h.repo)
				release := seedRelease(t, f.h.repo, f.project.ID)
				deployment := seedDeployment(t, f.h.repo, release.ID,
					f.environment.ID, "running")
				if _, err := f.h.repo.Queries.CreateDeploymentLog(t.Context(),
					db.CreateDeploymentLogParams{DeploymentID: deployment.ID,
						Line: "authorized-before-scope-change"}); err != nil {
					t.Fatal(err)
				}
				token := mintProjectToken(t, f, "viewer", f.project.ID)
				path := fmt.Sprintf(
					"/api/v1/deployments/%d/logs/stream?format=%s",
					deployment.ID,
					format,
				)
				request, err := http.NewRequestWithContext(
					t.Context(),
					"GET",
					f.baseURL+path,
					nil,
				)
				if err != nil {
					t.Fatal(err)
				}
				request.Header.Set("Authorization", "Bearer "+token.ID)
				response, err := f.client.Do(request)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				if response.StatusCode != 200 {
					t.Fatalf("stream status=%d", response.StatusCode)
				}
				closed := make(chan error, 1)
				go func() { _, err := io.Copy(io.Discard, response.Body); closed <- err }()
				switch change {
				case "scope":
					f.api(
						t,
						"PATCH",
						"/api/v1/tokens/"+token.TokenID,
						map[string]any{
							"scope":       "scoped",
							"role":        "viewer",
							"project_ids": []int64{other.ID},
						},
						200,
					)
				case "revoke":
					f.api(
						t,
						"DELETE",
						"/api/v1/tokens/"+token.TokenID,
						nil,
						204,
					)
				case "expire":
					if _, err := f.h.repo.DB.ExecContext(
						t.Context(),
						"UPDATE api_tokens SET expires_at = ? WHERE id = ?",
						time.Now().
							Add(-time.Minute).
							Unix(),
						token.TokenID,
					); err != nil {
						t.Fatal(err)
					}
				}
				select {
				case err := <-closed:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(3 * time.Second):
					t.Fatal(
						"stream remained open after token authorization changed",
					)
				}
			})
		}
	}
}
