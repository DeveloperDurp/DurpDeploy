//go:build e2e

package api_test

import (
	"encoding/json"
	"testing"

	"durpdeploy/internal/db"
)

func TestAdminStatsCountsAPIE2E(t *testing.T) {
	for _, backend := range []string{"SQLite", "PostgreSQL", "SQLServer"} {
		t.Run(backend, func(t *testing.T) {
			f, _ := newProvenanceE2E(t, provenanceDSN(t, backend))
			for _, want := range []struct{ Users, Projects, Deployments int64 }{
				{1, 1, 0}, {2, 3, 1},
			} {
				if want.Users == 2 {
					seedProject(t, f.h.repo)
					seedProject(t, f.h.repo)
					seedAPIUser(t, f.h.repo, "stats@example.test", "viewer")
					release, err := f.h.repo.Queries.CreateRelease(
						t.Context(),
						db.CreateReleaseParams{
							ProjectID: f.project.ID, Version: "stats", StepsJson: "[]",
						},
					)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := f.h.repo.Queries.CreateDeployment(t.Context(), db.CreateDeploymentParams{
						ReleaseID: release.ID, EnvironmentID: f.environment.ID, Status: "succeeded",
					}); err != nil {
						t.Fatal(err)
					}
				}
				var got struct{ Users, Projects, Deployments int64 }
				if err := json.Unmarshal(f.api(t, "GET", "/api/v1/admin/stats", nil, 200), &got); err != nil {
					t.Fatal(err)
				}
				if got != want {
					t.Fatalf("stats=%+v want=%+v", got, want)
				}
			}
		})
	}
}
