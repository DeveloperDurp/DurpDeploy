package agentserver_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/dispatch"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestAgentDurableOutcomeAfterMaintenanceAndRestart(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for _, outcome := range []struct {
			state  string
			cancel bool
		}{
			{"cleanup_unconfirmed", false},
			{"cleanup_unconfirmed", true},
			{"succeeded", false},
			{"failed", false},
			{"cancelled", true},
		} {
			state := outcome.state
			t.Run(
				fmt.Sprintf(
					"legacy=%v/%s/cancel=%v",
					legacy,
					state,
					outcome.cancel,
				),
				func(t *testing.T) {
					// Given a started container claim whose agent stops responding.
					dsn := filepath.Join(t.TempDir(), "recovery.db") +
						"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
					f := newAgentFixtureWithDSN(t, dsn, nil)
					var id int64
					var claim, table string
					if legacy {
						id = seedPollPayload(t, f, "pending", "test-agent")
						if _, err := f.repo.DB.ExecContext(t.Context(), `UPDATE deployment_steps
						SET agent_execution_mode='container', container_image='alpine:3.20',
						variable_names='[]' WHERE deployment_id=?`, id); err != nil {
							t.Fatal(err)
						}
						poll := decodePollResponse(
							t,
							postAgent(
								t,
								f,
								agentproto.PollPath,
								containerPollBody,
							),
						)
						claim = fmt.Sprintf(
							`{"protocol":"agent/3","claim_token":%q}`,
							poll.ClaimToken,
						)
						table = "remote_deployment_claims"
					} else {
						id, claim = claimedRemoteStepWithMode(t, f, "container", containerPollBody)
						table = "remote_step_runs"
					}
					path := func(pattern string) string {
						return strings.ReplaceAll(
							pattern,
							"{id}",
							fmt.Sprint(id),
						)
					}
					assertAgentStatus(
						t,
						postAgent(t, f, path(agentproto.StartPath), claim),
						204,
					)
					if outcome.cancel {
						if _, err := f.repo.DB.ExecContext(t.Context(), "UPDATE "+table+
							" SET state='cancel_requested', cancel_requested_at=1 WHERE deployment_id=?", id); err != nil {
							t.Fatal(err)
						}
					}
					if _, err := f.repo.DB.ExecContext(t.Context(), "UPDATE "+table+
						" SET last_heartbeat_at=1 WHERE deployment_id=?", id); err != nil {
						t.Fatal(err)
					}
					if err := dispatch.New(f.repo).Maintain(t.Context()); err != nil {
						t.Fatal(err)
					}
					assertDeploymentStatus(t, f, id, "failed")
					deployment, err := f.repo.Queries.GetDeployment(
						t.Context(),
						id,
					)
					if err != nil {
						t.Fatal(err)
					}
					next, err := f.repo.Queries.CreateDeployment(
						t.Context(),
						db.CreateDeploymentParams{
							ReleaseID: deployment.ReleaseID, EnvironmentID: deployment.EnvironmentID, Status: "queued",
						},
					)
					if err != nil {
						t.Fatal(err)
					}
					if err := f.repo.DB.Close(); err != nil {
						t.Fatal(err)
					}
					conn, err := migrate.Run(dsn)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { conn.Close() })
					*f.repo = *repository.New(conn)
					if err := f.repo.ReconcileDeploymentQueues(t.Context()); err != nil {
						t.Fatal(err)
					}
					assertDeploymentStatus(t, f, next.ID, "queued")

					// When the restarted agent replays its durable authenticated outcome.
					endpoint := path(agentproto.ResultPath)
					body := strings.TrimSuffix(
						claim,
						"}",
					) + fmt.Sprintf(
						`,"state":%q,"error":""}`,
						state,
					)
					if state == "cancelled" {
						endpoint, body = path(agentproto.CancelledPath), claim
					}
					assertAgentStatus(t, postAgent(
						t,
						f,
						endpoint,
						strings.Replace(
							body,
							stringClaimToken(claim),
							"wrong-token",
							1,
						),
					), 409)
					for range 2 {
						assertAgentStatus(
							t,
							postAgent(t, f, endpoint, body),
							204,
						)
					}
					changedOutcome := "succeeded"
					if state == changedOutcome {
						changedOutcome = "failed"
					}
					assertAgentStatus(
						t,
						postAgent(
							t,
							f,
							path(agentproto.ResultPath),
							strings.TrimSuffix(
								claim,
								"}",
							)+fmt.Sprintf(
								`,"state":%q,"error":""}`,
								changedOutcome,
							),
						),
						409,
					)
					var stored string
					if err := conn.QueryRowContext(t.Context(), "SELECT state FROM "+table+
						" WHERE deployment_id=?", id).Scan(&stored); err != nil ||
						stored != state {
						t.Fatalf("stored state=%s error=%v", stored, err)
					}

					// Then success cannot revive the failed deployment; uncertainty still blocks cleanup.
					if err := dispatch.New(f.repo).Maintain(t.Context()); err != nil {
						t.Fatal(err)
					}
					if state == "cleanup_unconfirmed" {
						assertDeploymentStatus(t, f, next.ID, "queued")
						assertDeploymentStatus(t, f, id, "cleanup_unconfirmed")
						var ready agentproto.PollRequest
						if err := ready.UnmarshalJSON([]byte(containerPollBody)); err != nil {
							t.Fatal(err)
						}
						recordFixturePoll(t, f, ready)
					} else {
						assertDeploymentStatus(t, f, id, "failed")
					}
					assertDeploymentStatus(t, f, next.ID, "pending")
					assertAgentStatus(t, postAgent(t, f, endpoint, body), 204)
				},
			)
		}
	}
}
