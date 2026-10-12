package repository

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"durpdeploy/internal/db"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestListQueriesAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		repo, _ := openDeploymentCreationEngine(t,
			newDeploymentCreationEngine(t, name))
		t.Run("lifecycle counts", func(t *testing.T) {
			testLifecycleCounts(t, repo)
		})
		t.Run("schedule joins", func(t *testing.T) {
			testScheduleDetails(t, repo)
		})
		t.Run("selector batches", func(t *testing.T) {
			testSelectorBatches(t, repo)
		})
		t.Run("release activity", func(t *testing.T) {
			testReleaseActivity(t, repo)
		})
		t.Run("agent lookup and poll", func(t *testing.T) {
			agent, err := repo.Queries.GetAgentByCertificateFingerprint(
				t.Context(), sql.NullString{
					String: strings.Repeat("a", 64), Valid: true,
				})
			if err != nil || agent.ID != "race-agent" {
				t.Fatalf("fingerprint lookup=%s error=%v", agent.ID, err)
			}
			report := AgentPollCapabilities{PollRequest: agentproto.PollRequest{
				ProtocolEnvelope: agentproto.ProtocolEnvelope{
					Protocol: agentproto.AgentV1,
				},
			}}
			heartbeat := db.HeartbeatAgentParams{
				ID: agent.ID, CertificateFingerprint: agent.CertificateFingerprint,
				Now: sql.NullInt64{Int64: 200, Valid: true},
			}
			for range 2 {
				heartbeat.Now.Int64++
				if changed, err := repo.RecordAgentPollCapabilities(
					t.Context(), heartbeat, report,
				); err != nil || changed != 1 {
					t.Fatalf("normalized poll rows=%d error=%v", changed, err)
				}
			}
		})
	})
}

func testLifecycleCounts(t *testing.T, repo *Repository) {
	t.Helper()
	for _, name := range []string{"Zulu", "Alpha"} {
		lifecycle, err := repo.Queries.CreateLifecycle(t.Context(),
			db.CreateLifecycleParams{Name: name})
		if err != nil {
			t.Fatal(err)
		}
		if name == "Zulu" {
			if _, err := repo.Queries.CreateLifecycleStage(t.Context(),
				db.CreateLifecycleStageParams{
					LifecycleID: lifecycle.ID, EnvironmentID: 1,
				}); err != nil {
				t.Fatal(err)
			}
		}
	}
	rows, err := repo.Queries.ListLifecyclesWithStageCounts(t.Context())
	if err != nil || len(rows) != 2 {
		t.Fatalf("lifecycle count=%d error=%v", len(rows), err)
	}
	if rows[0].Name != "Alpha" || rows[0].StageCount != 0 ||
		rows[1].Name != "Zulu" || rows[1].StageCount != 1 {
		t.Fatalf("lifecycle rows=%+v", rows)
	}
}

func testScheduleDetails(t *testing.T, repo *Repository) {
	t.Helper()
	var latest int64
	for range 3 {
		row, err := repo.Queries.CreateScheduledDeployment(t.Context(),
			db.CreateScheduledDeploymentParams{
				ProjectID: 1, ReleaseID: 1, EnvironmentID: 1,
				Cron: "0 0 * * *", NextRunAt: 4_000_000_000,
			})
		if err != nil {
			t.Fatal(err)
		}
		latest = row.ID
	}
	// Force a timestamp tie to prove stable page boundaries on every engine.
	if _, err := repo.DB.ExecContext(t.Context(),
		"UPDATE scheduled_deployments SET created_at = 100"); err != nil {
		t.Fatal(err)
	}
	for offset := int64(0); offset < 3; offset++ {
		rows, err := repo.Queries.ListScheduledDeploymentDetails(t.Context(),
			db.ListScheduledDeploymentDetailsParams{
				ProjectID: 1, Limit: 1, Offset: offset,
			})
		if err != nil || len(rows) != 1 {
			t.Fatalf(
				"schedule page=%d count=%d error=%v",
				offset,
				len(rows),
				err,
			)
		}
		if rows[0].ScheduledDeployment.ID != latest-offset ||
			rows[0].ReleaseVersion != "v1" ||
			rows[0].EnvironmentName != "creation-race" {
			t.Fatalf("schedule page=%d row=%+v", offset, rows[0])
		}
	}
}

func testSelectorBatches(t *testing.T, repo *Repository) {
	t.Helper()
	if rows, err := StepSelectors(t.Context(), repo.Queries, nil); err != nil ||
		len(rows) != 0 {
		t.Fatalf("empty selectors=%v error=%v", rows, err)
	}
	ids := make([]int64, 2101)
	for index := range ids {
		ids[index] = int64(index + 10000)
	}
	for _, index := range []int{0, 499, 500, 2100} {
		step, err := repo.CreateStepWithPlacement(
			t.Context(),
			db.CreateStepParams{
				ProjectID: 1, Name: fmt.Sprintf("batch-%d", index),
				Interpreter: "bash", AgentExecutionMode: "host",
				NetworkMode: "",
			},
			"agent",
			[]string{"zulu", "alpha"},
		)
		if err != nil {
			t.Fatal(err)
		}
		ids[index] = step.ID
	}
	rows, err := StepSelectors(t.Context(), repo.Queries, ids)
	if err != nil || len(rows) != 4 {
		t.Fatalf("batched selectors=%v error=%v", rows, err)
	}
	for _, labels := range rows {
		if !reflect.DeepEqual(labels, []string{"alpha", "zulu"}) {
			t.Fatalf("selector order=%v", labels)
		}
	}
}

func testReleaseActivity(t *testing.T, repo *Repository) {
	t.Helper()
	deployment := createLegacyRemoteDeployment(t, repo)
	for _, state := range []string{
		"queued", "pending", "running", "pending_approval",
		"publishing_artifact", "awaiting_artifact_approval",
		"cleanup_unconfirmed", "succeeded", "failed", "cancelled",
	} {
		if err := repo.Queries.UpdateDeploymentStatus(t.Context(),
			db.UpdateDeploymentStatusParams{ID: deployment.ID, Status: state},
		); err != nil {
			t.Fatal(err)
		}
		assertReleaseListActivity(t, repo)
	}
	for _, state := range []string{
		"claimed", "started", "cancel_requested", "lost", "cancel_unconfirmed",
		"cleanup_unconfirmed", "succeeded",
	} {
		started := sql.NullInt64{Int64: 100, Valid: state != "claimed"}
		finished := sql.NullInt64{Int64: 150, Valid: state == "lost" ||
			state == "cancel_unconfirmed" || state == "cleanup_unconfirmed" ||
			state == "succeeded"}
		cancelled := sql.NullInt64{Int64: 125,
			Valid: state == "cancel_requested" || state == "cancel_unconfirmed"}
		for _, confirmed := range []sql.NullInt64{{}, {Int64: 200, Valid: true}} {
			if _, err := repo.DB.ExecContext(t.Context(),
				"UPDATE remote_deployment_claims SET state=?, cleanup_confirmed_at=?, claim_token_hash=?, started_at=?, finished_at=?, cancel_requested_at=?, ciphertext='fixture', claim_expires_at=500, last_heartbeat_at=100 WHERE deployment_id=?",
				state, confirmed, make([]byte, 32), started, finished, cancelled, deployment.ID); err != nil {
				t.Fatal(err)
			}
			assertReleaseListActivity(t, repo)
		}
	}
}

func assertReleaseListActivity(t *testing.T, repo *Repository) {
	t.Helper()
	rows, err := repo.Queries.ListReleasesWithActivity(t.Context(), 1)
	if err != nil || len(rows) != 1 {
		t.Fatalf("release list count=%d error=%v", len(rows), err)
	}
	active, err := repo.Queries.HasActiveReleaseDeployment(
		t.Context(),
		rows[0].ID,
	)
	if err != nil || rows[0].Active != active {
		t.Fatalf(
			"list activity=%d authoritative=%d error=%v",
			rows[0].Active,
			active,
			err,
		)
	}
}
