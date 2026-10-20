package repository

import (
	"context"
	"errors"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func queuedCancellationRepositoryFixture(
	t *testing.T,
	repo *Repository,
) (int64, int64) {
	t.Helper()
	release, err := repo.Queries.CreateRelease(
		t.Context(),
		db.CreateReleaseParams{
			ProjectID: 1, Version: "queued-log-reasons",
			StepsJson: `[{"name":"duplicate","script_body":"true","container_image":"bash:5.2"},` +
				`{"name":"duplicate","script_body":"true","container_image":"bash:5.2"}]`,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	create := func() int64 {
		t.Helper()
		result, err := repo.CreateDeployment(
			t.Context(),
			db.CreateDeploymentParams{
				ReleaseID: release.ID, EnvironmentID: 1, Status: "pending",
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		return result.Deployment.ID
	}
	return create(), create()
}

func TestQueuedCancellationLogsAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		repo, restarted := openDeploymentCreationEngine(t,
			newDeploymentCreationEngine(t, name))
		owner, queued := queuedCancellationRepositoryFixture(t, repo)
		if err := repo.CancelQueuedDeployment(t.Context(), queued); err != nil {
			t.Fatal(err)
		}
		if err := restarted.CancelQueuedDeployment(t.Context(), queued); !errors.Is(
			err,
			ErrDeploymentQueueConflict,
		) {
			t.Fatalf("repeated cancellation error=%v", err)
		}
		logs, err := restarted.Queries.ListDeploymentLogsAfter(t.Context(),
			db.ListDeploymentLogsAfterParams{DeploymentID: queued})
		if err != nil || len(logs) != 2 {
			t.Fatalf("persisted cancellation records=%+v error=%v", logs, err)
		}
		for i, log := range logs {
			if log.StepIndex.Int64 != int64(i) || !log.StepIndex.Valid ||
				log.StepName.String != "duplicate" ||
				log.StepState.String != "cancelled" || log.Line != QueuedCancellationLogLine {
				t.Fatalf("cancellation record=%+v", log)
			}
		}
		states, err := restarted.Queries.ListDeploymentLogStateMetadata(
			t.Context(),
			db.ListDeploymentLogStateMetadataParams{
				DeploymentID: queued, LastID: logs[1].ID,
				QueuedCancellationLine: QueuedCancellationLogLine,
			},
		)
		if err != nil || len(states) != 2 ||
			states[0].Line != QueuedCancellationLogLine ||
			states[1].Line != QueuedCancellationLogLine {
			t.Fatalf("bounded cancellation metadata=%+v error=%v", states, err)
		}
		active, err := restarted.Queries.GetDeployment(t.Context(), owner)
		if err != nil || active.Status != "pending" {
			t.Fatalf("owner changed=%+v error=%v", active, err)
		}
	})
}

func TestQueuedCancellationLogFailureRollsBack(t *testing.T) {
	repo, _ := openDeploymentCreationEngine(t,
		newDeploymentCreationEngine(t, "SQLite"))
	_, queued := queuedCancellationRepositoryFixture(t, repo)
	if _, err := repo.DB.ExecContext(t.Context(), `
CREATE TRIGGER reject_queued_log BEFORE INSERT ON deployment_logs
WHEN NEW.step_index = 1 BEGIN SELECT RAISE(ABORT, 'queued log failed'); END`); err != nil {
		t.Fatal(err)
	}
	if err := repo.CancelQueuedDeployment(t.Context(), queued); err == nil ||
		!strings.Contains(err.Error(), "queued log failed") {
		t.Fatalf("log write error=%v", err)
	}
	deployment, err := repo.Queries.GetDeployment(t.Context(), queued)
	if err != nil || deployment.Status != "queued" ||
		deployment.FinishedAt.Valid {
		t.Fatalf(
			"failed transaction changed cancellation=%+v error=%v",
			deployment,
			err,
		)
	}
	logs, err := repo.Queries.ListDeploymentLogsAfter(t.Context(),
		db.ListDeploymentLogsAfterParams{DeploymentID: queued})
	if err != nil || len(logs) != 0 {
		t.Fatalf("partial records survived=%+v error=%v", logs, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := repo.CancelQueuedDeployment(ctx, queued); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Fatalf("cancelled request error=%v", err)
	}
}
