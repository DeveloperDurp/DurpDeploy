package api_test

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/handler/api"
)

func TestRefreshReleaseAfterDeployment(t *testing.T) {
	for _, scenario := range []struct{ state, buffer, remoteState string }{
		{state: "failed"}, {state: "succeeded"},
		{state: "pending"}, {state: "running"},
		{state: "cleanup_unconfirmed"},
		{state: "failed", buffer: "remote_deployment_claims"},
		{state: "succeeded", buffer: "remote_step_runs"},
		{state: "failed", buffer: "remote_deployment_claims", remoteState: "cleanup_unconfirmed"},
		{state: "failed", buffer: "remote_step_runs", remoteState: "cleanup_unconfirmed"},
	} {
		name := fmt.Sprintf("%s/%s/%s", scenario.state, scenario.buffer,
			scenario.remoteState)
		t.Run(name, func(t *testing.T) {
			// Given: a deployed release with the historical lock flag.
			h := newAPIHarness(t)
			user := seedAPIUser(t, h.repo, "admin@example.com", "admin")
			project := seedProject(t, h.repo)
			environment := seedEnv(t, h.repo)
			step, err := h.repo.Queries.CreateStep(
				t.Context(), db.CreateStepParams{
					ProjectID: project.ID, Name: "deploy",
					ScriptBody: "echo before", ContainerImage: "alpine:3.20",
				})
			if err != nil {
				t.Fatal(err)
			}
			release, err := handler.CreateReleaseSnapshot(
				t.Context(), h.repo, project.ID, "refresh-used")
			if err != nil {
				t.Fatal(err)
			}
			deployment, err := h.repo.CreateDeployment(
				t.Context(), db.CreateDeploymentParams{
					ReleaseID: release.ID, EnvironmentID: environment.ID,
					Status: "pending",
				})
			if err != nil {
				t.Fatal(err)
			}
			if err := h.repo.Queries.UpdateDeploymentStatus(
				t.Context(), db.UpdateDeploymentStatusParams{
					ID: deployment.Deployment.ID, Status: scenario.state,
				}); err != nil {
				t.Fatal(err)
			}
			if scenario.buffer != "" {
				if _, err := h.repo.DB.ExecContext(t.Context(),
					`INSERT INTO agents(id, name, endpoint)
VALUES('buffer-agent', 'Buffer', 'https://agent.example')`); err != nil {
					t.Fatal(err)
				}
				statement := `INSERT INTO remote_deployment_claims(
deployment_id, agent_id, state, claim_token_hash, ciphertext,
claim_expires_at, last_heartbeat_at, started_at, finished_at,
log_buffer_ciphertext)
VALUES(?, 'buffer-agent', ?, zeroblob(32), 'fixture', 1, 1, 1, 2, ?)`
				if scenario.buffer == "remote_step_runs" {
					statement = `INSERT INTO remote_step_runs(
deployment_id, step_index, agent_id, state, log_buffer_ciphertext)
VALUES(?, 0, 'buffer-agent', ?, ?)`
				}
				remoteState := scenario.remoteState
				buffer := sql.NullString{
					String: "buffered-fixture",
					Valid:  true,
				}
				if remoteState == "" {
					remoteState = "failed"
				} else {
					buffer = sql.NullString{}
				}
				if _, err := h.repo.DB.ExecContext(t.Context(), statement,
					deployment.Deployment.ID, remoteState, buffer); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := h.repo.DB.ExecContext(t.Context(),
				"UPDATE releases SET snapshot_locked=1 WHERE id=?",
				release.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := h.repo.DB.ExecContext(t.Context(),
				"UPDATE steps SET script_body='echo after' WHERE id=?",
				step.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := h.repo.Queries.CreateVariable(
				t.Context(), db.CreateVariableParams{
					ProjectID: project.ID, Name: "REFRESHED",
					Value: sql.NullString{String: "after", Valid: true},
				}); err != nil {
				t.Fatal(err)
			}

			// When: the API refreshes the same release.
			req := httptest.NewRequest(http.MethodPost,
				fmt.Sprintf("/api/v1/projects/%d/releases/%d/refresh",
					project.ID, release.ID), nil)
			req = withAPIUser(req, user)
			req = withAPIURLParam(req, "id", fmt.Sprint(project.ID))
			req = withAPIURLParam(req, "relId", fmt.Sprint(release.ID))
			rec := httptest.NewRecorder()
			api.NewReleaseHandler(h.repo).RefreshRelease(rec, req)
			if scenario.state == "pending" || scenario.state == "running" ||
				scenario.state == "cleanup_unconfirmed" || scenario.buffer != "" {
				if rec.Code != http.StatusConflict {
					t.Fatalf(
						"busy refresh=%d: %s",
						rec.Code,
						rec.Body.String(),
					)
				}
				unchanged, err := h.repo.Queries.GetRelease(
					t.Context(),
					release.ID,
				)
				if err != nil || unchanged.StepsJson != release.StepsJson {
					t.Fatalf("busy refresh changed steps: %v", err)
				}
				variables, err := h.repo.ListReleaseVariablesByRelease(
					t.Context(), release.ID)
				if err != nil || len(variables) != 0 {
					t.Fatalf(
						"busy refresh changed variables: %+v %v",
						variables,
						err,
					)
				}
				if scenario.buffer != "" {
					if _, err := h.repo.DB.ExecContext(t.Context(),
						"UPDATE "+scenario.buffer+
							" SET log_buffer_ciphertext=NULL, cleanup_confirmed_at=1"); err != nil {
						t.Fatal(err)
					}
				}
				if err := h.repo.Queries.UpdateDeploymentStatus(t.Context(),
					db.UpdateDeploymentStatusParams{
						ID: deployment.Deployment.ID, Status: "failed",
					}); err != nil {
					t.Fatal(err)
				}
				rec = httptest.NewRecorder()
				api.NewReleaseHandler(h.repo).RefreshRelease(rec, req)
			}

			// Then: the release changes; historical deployment steps do not.
			if rec.Code != http.StatusOK {
				t.Fatalf("refresh=%d: %s", rec.Code, rec.Body.String())
			}
			updated, err := h.repo.Queries.GetRelease(
				t.Context(),
				release.ID,
			)
			if err != nil ||
				!strings.Contains(updated.StepsJson, "echo after") {
				t.Fatalf(
					"refreshed steps=%s error=%v",
					updated.StepsJson,
					err,
				)
			}
			variables, err := h.repo.ListReleaseVariablesByRelease(
				t.Context(), release.ID)
			if err != nil || len(variables) != 1 ||
				variables[0].Value.String != "after" {
				t.Fatalf("refreshed variables=%+v error=%v", variables, err)
			}
			steps, err := h.repo.Queries.ListDeploymentSteps(
				t.Context(), deployment.Deployment.ID)
			if err != nil || len(steps) != 1 ||
				steps[0].ScriptBody != "echo before" {
				t.Fatalf("historical steps=%+v error=%v", steps, err)
			}
		})
	}
}
