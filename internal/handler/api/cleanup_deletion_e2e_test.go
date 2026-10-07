//go:build e2e

package api_test

import (
	"database/sql"
	"fmt"
	"net/http"
	"testing"

	"durpdeploy/internal/db"
)

func TestVerificationCleanupDeletionAPIWebE2E(t *testing.T) {
	for _, resource := range []string{"projects", "environments"} {
		for _, transport := range []string{"api", "web"} {
			for _, remote := range []string{"step", "deployment"} {
				t.Run(resource+"/"+transport+"/"+remote, func(t *testing.T) {
					f := newVerificationE2E(t)
					release := verificationRelease(t, f, "cleanup-delete")
					deployment, err := f.h.repo.CreateDeployment(t.Context(),
						db.CreateDeploymentParams{
							ReleaseID:     release.ID,
							EnvironmentID: f.environment.ID,
							Status:        "pending",
						})
					if err != nil {
						t.Fatal(err)
					}
					if _, err := f.h.repo.DB.Exec(
						`INSERT INTO agents(id,name,endpoint)
VALUES('cleanup','Cleanup','https://agent.example')`,
					); err != nil {
						t.Fatal(err)
					}
					statement := `INSERT INTO remote_step_runs(deployment_id,step_index,agent_id,state)
VALUES(?,0,'cleanup','cleanup_unconfirmed')`
					table := "remote_step_runs"
					if remote == "deployment" {
						statement = `INSERT INTO remote_deployment_claims(deployment_id,agent_id,state,
claim_token_hash,ciphertext,claim_expires_at,last_heartbeat_at,started_at,finished_at)
VALUES(?,'cleanup','cleanup_unconfirmed',zeroblob(32),'fixture',1,1,1,1)`
						table = "remote_deployment_claims"
					}
					if _, err := f.h.repo.DB.Exec(
						statement,
						deployment.Deployment.ID,
					); err != nil {
						t.Fatal(err)
					}
					id := f.project.ID
					if resource == "environments" {
						id = f.environment.ID
					}
					path := fmt.Sprintf("/%s/%d", resource, id)
					// Both a failed parent and a cleanup parent must preserve uncertain work.
					for _, state := range []string{"failed", "cleanup_unconfirmed"} {
						if _, err := f.h.repo.DB.Exec(
							"UPDATE deployments SET status=? WHERE id=?",
							state,
							deployment.Deployment.ID,
						); err != nil {
							t.Fatal(err)
						}
						if state == "failed" {
							for _, active := range []string{"claimed", "started", "cancel_requested"} {
								started := sql.NullInt64{
									Int64: 1,
									Valid: active != "claimed",
								}
								cancelled := sql.NullInt64{
									Int64: 1,
									Valid: active == "cancel_requested",
								}
								if _, err := f.h.repo.DB.Exec(
									"UPDATE "+table+" SET state=?,started_at=?,finished_at=NULL,cancel_requested_at=?",
									active,
									started,
									cancelled,
								); err != nil {
									t.Fatal(err)
								}
								f.api(
									t,
									"DELETE",
									"/api/v1"+path,
									nil,
									http.StatusConflict,
								)
								f.web(
									t,
									"DELETE",
									path,
									nil,
									http.StatusConflict,
								)
								refresh := fmt.Sprintf(
									"%s/releases/%d/refresh",
									f.base(),
									release.ID,
								)
								f.api(
									t,
									"POST",
									refresh,
									nil,
									http.StatusConflict,
								)
								f.web(
									t,
									"POST",
									refresh[len("/api/v1"):],
									nil,
									http.StatusConflict,
								)
							}
							if _, err := f.h.repo.DB.Exec(
								"UPDATE " + table + " SET state='cleanup_unconfirmed',started_at=1,finished_at=1,cancel_requested_at=NULL",
							); err != nil {
								t.Fatal(err)
							}
						}
						f.api(
							t,
							"DELETE",
							"/api/v1"+path,
							nil,
							http.StatusConflict,
						)
						f.web(t, "DELETE", path, nil, http.StatusConflict)
					}
					if _, err := f.h.repo.DB.Exec(
						"UPDATE " + table + " SET cleanup_confirmed_at=1",
					); err != nil {
						t.Fatal(err)
					}
					if transport == "api" {
						f.api(
							t,
							"DELETE",
							"/api/v1"+path,
							nil,
							http.StatusNoContent,
						)
					} else {
						status := http.StatusOK
						if resource == "projects" {
							status = http.StatusSeeOther
						}
						f.web(t, "DELETE", path, nil, status)
					}
					var found int
					if err := f.h.repo.DB.QueryRow("SELECT id FROM "+resource+" WHERE id=?", id).
						Scan(&found); err != sql.ErrNoRows {
						t.Fatalf("deleted resource still exists: %v", err)
					}
				})
			}
		}
	}
}
