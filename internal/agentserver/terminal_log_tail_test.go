package agentserver_test

import (
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/httpstream"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestAgentTerminalCommitsBufferedTailAtomically(t *testing.T) {
	for _, step := range []bool{false, true} {
		for _, cancelled := range []bool{false, true} {
			t.Run(
				fmt.Sprintf("step=%t/cancelled=%t", step, cancelled),
				func(t *testing.T) {
					f := newAgentFixture(t)
					id, claim := int64(
						1,
					), `{"protocol":"agent/1","claim_token":"claim-one"}`
					if step {
						id, claim = claimedRemoteStep(t, f)
					} else {
						seedAgentLifecycle(t, f.repo)
					}
					path := func(pattern string) string { return strings.ReplaceAll(pattern, "{id}", fmt.Sprint(id)) }
					if response := postAgent(
						t,
						f,
						path(agentproto.StartPath),
						claim,
					); response.StatusCode != 204 {
						t.Fatalf("start=%d", response.StatusCode)
					}
					deployment, err := f.repo.Queries.GetDeployment(
						t.Context(),
						id,
					)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := f.repo.DB.ExecContext(
						t.Context(),
						`INSERT INTO release_variables(release_id,name,value,secret) VALUES(?,'TAIL','tail-secret',1)`,
						deployment.ReleaseID,
					); err != nil {
						t.Fatal(err)
					}
					body := strings.TrimSuffix(
						claim,
						"}",
					) + `,"events":[{"sequence":1,"line":"tail-"}]}`
					if response := postAgent(
						t,
						f,
						path(agentproto.LogsPath),
						body,
					); response.StatusCode != 204 {
						t.Fatalf("logs=%d", response.StatusCode)
					}
					var queued db.Deployment
					if !step {
						next, err := f.repo.CreateDeployment(
							t.Context(),
							db.CreateDeploymentParams{
								ReleaseID:     deployment.ReleaseID,
								EnvironmentID: deployment.EnvironmentID,
								Status:        "pending",
							},
						)
						if err != nil || next.Deployment.Status != "queued" {
							t.Fatalf("queue admission=%+v: %v", next, err)
						}
						queued = next.Deployment
					}
					terminalPath, terminalBody, expected := agentproto.ResultPath, strings.TrimSuffix(
						claim,
						"}",
					)+`,"state":"succeeded","error":""}`, "started"
					if cancelled {
						terminalPath, terminalBody, expected = agentproto.CancelledPath, claim, "cancel_requested"
						if step {
							_, err = f.repo.Queries.RequestRemoteStepCancellation(
								t.Context(),
								db.RequestRemoteStepCancellationParams{
									DeploymentID: id,
									Now: sql.NullInt64{
										Int64: 1,
										Valid: true,
									},
								},
							)
						} else {
							_, err = f.repo.DB.ExecContext(
								t.Context(),
								"UPDATE remote_deployment_claims SET state='cancel_requested',cancel_requested_at=unixepoch() WHERE deployment_id=?",
								id,
							)
						}
						if err != nil {
							t.Fatal(err)
						}
					}
					if _, err := f.repo.DB.Exec(
						`CREATE TRIGGER reject_tail BEFORE INSERT ON deployment_logs BEGIN SELECT RAISE(ABORT,'tail write failed'); END`,
					); err != nil {
						t.Fatal(err)
					}
					if response := postAgent(
						t,
						f,
						path(terminalPath),
						terminalBody,
					); response.StatusCode != 500 {
						t.Fatalf("failed flush=%d", response.StatusCode)
					}
					if !step {
						owner, err := f.repo.Queries.GetDeploymentSlot(
							t.Context(),
							deployment.ID,
						)
						if err != nil || owner != id {
							t.Fatalf(
								"failed flush released environment: owner=%d: %v",
								owner,
								err,
							)
						}
					}
					table := "remote_deployment_claims"
					if step {
						table = "remote_step_runs"
					}
					var state string
					if err := f.repo.DB.QueryRowContext(t.Context(), "SELECT state FROM "+table+" WHERE deployment_id=?", id).
						Scan(&state); err != nil ||
						state != expected {
						t.Fatalf(
							"terminal escaped failed flush: state=%s err=%v",
							state,
							err,
						)
					}
					if _, err := f.repo.DB.Exec(
						"DROP TRIGGER reject_tail",
					); err != nil {
						t.Fatal(err)
					}
					if response := postAgent(
						t,
						f,
						path(terminalPath),
						terminalBody,
					); response.StatusCode != 204 {
						t.Fatalf("retry=%d", response.StatusCode)
					}
					if !step {
						owner, err := f.repo.Queries.GetDeploymentSlot(
							t.Context(),
							deployment.ID,
						)
						if err != nil || owner != queued.ID {
							t.Fatalf(
								"terminal did not advance queue: owner=%d, want %d: %v",
								owner,
								queued.ID,
								err,
							)
						}
					}
					if step {
						if _, err := f.repo.DB.ExecContext(
							t.Context(),
							"UPDATE deployments SET status='succeeded' WHERE id=?",
							id,
						); err != nil {
							t.Fatal(err)
						}
					}
					server := httptest.NewServer(
						http.HandlerFunc(
							func(w http.ResponseWriter, r *http.Request) { httpstream.StreamStepLogs(w, r, f.repo, f.broker, id) },
						),
					)
					defer server.Close()
					response, err := http.Get(server.URL)
					if err != nil {
						t.Fatal(err)
					}
					defer response.Body.Close()
					data, err := io.ReadAll(response.Body)
					if err != nil {
						t.Fatal(err)
					}
					text := string(data)
					tail, complete := strings.Index(
						text,
						"tail-",
					), strings.Index(
						text,
						"event: complete",
					)
					if tail < 0 || complete <= tail {
						t.Fatalf("tail missing before completion: %s", text)
					}
				},
			)
		}
	}
}
