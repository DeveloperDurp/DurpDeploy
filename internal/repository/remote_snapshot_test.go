package repository_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func TestDeploymentStepSnapshotPreservesLifecycle(t *testing.T) {
	for _, status := range []string{"pending", "pending_approval"} {
		for _, empty := range []bool{false, true} {
			t.Run(
				status+map[bool]string{false: "/steps", true: "/empty"}[empty],
				func(t *testing.T) {
					r := remoteFixture(t)
					ctx := context.Background()
					d, err := r.Queries.CreateDeployment(
						ctx,
						db.CreateDeploymentParams{
							ReleaseID: 1, EnvironmentID: 1, Status: status,
						},
					)
					if err != nil {
						t.Fatal(err)
					}
					steps := []repository.DeploymentStepSnapshot{{
						CreateDeploymentStepParams: db.CreateDeploymentStepParams{
							Name:            "original",
							ScriptBody:      "echo original",
							ExecutionTarget: "agent",
						}, Selectors: []string{" LINUX ", "linux"},
					}}
					if empty {
						steps = nil
					}
					if err := r.SnapshotDeploymentSteps(ctx, d.ID, steps); err != nil {
						t.Fatalf("snapshot before execution rejected: %v", err)
					}
					got, err := r.Queries.GetDeployment(ctx, d.ID)
					if err != nil {
						t.Fatal(err)
					}
					if got.Status != status || got.StartedAt != d.StartedAt {
						t.Errorf(
							"snapshot changed lifecycle: status=%s started=%v",
							got.Status,
							got.StartedAt.Valid,
						)
					}
					source, err := r.Queries.GetDeploymentStepSource(ctx, d.ID)
					if err != nil || source.StepsJson != `[{"name":"frozen"}]` {
						t.Fatalf("source=%+v error=%v", source, err)
					}
					before, err := r.Queries.ListDeploymentSteps(ctx, d.ID)
					if err != nil || len(before) != len(steps) {
						t.Fatalf("steps=%v error=%v", before, err)
					}
					if !empty &&
						(before[0].Name != "original" || before[0].ScriptBody != "echo original") {
						t.Fatal("snapshot content differs")
					}
					if _, err := r.DB.ExecContext(ctx, "UPDATE releases SET steps_json = '[]' WHERE id = 1"); err != nil {
						t.Fatal(err)
					}
					for _, replacement := range [][]repository.DeploymentStepSnapshot{nil, {{CreateDeploymentStepParams: db.CreateDeploymentStepParams{Name: "replacement", ExecutionTarget: "local"}}}} {
						if err := r.SnapshotDeploymentSteps(ctx, d.ID, replacement); !errors.Is(
							err,
							sql.ErrNoRows,
						) {
							t.Fatalf("duplicate snapshot error=%v", err)
						}
					}
					n, err := r.Queries.CreateDeploymentStep(
						ctx,
						db.CreateDeploymentStepParams{
							DeploymentID: d.ID, StepIndex: int64(len(steps)), Name: "append", ExecutionTarget: "local",
						},
					)
					assertZero(t, n, err)
					if !empty {
						n, err := r.Queries.AddDeploymentStepSelector(
							ctx,
							db.AddDeploymentStepSelectorParams{
								DeploymentID: d.ID,
								StepIndex:    0,
								Label:        "new",
							},
						)
						assertZero(t, n, err)
						labels, err := r.Queries.ListDeploymentStepSelectors(
							ctx,
							db.ListDeploymentStepSelectorsParams{
								DeploymentID: d.ID,
								StepIndex:    0,
							},
						)
						if err != nil ||
							!reflect.DeepEqual(labels, []string{"linux"}) {
							t.Fatalf("labels=%v error=%v", labels, err)
						}
					}
					after, err := r.Queries.ListDeploymentSteps(ctx, d.ID)
					if err != nil || !reflect.DeepEqual(before, after) {
						t.Fatalf("snapshot replaced: %v", err)
					}
					frozen, err := r.Queries.GetDeploymentStepSource(ctx, d.ID)
					if err != nil || frozen != source {
						t.Fatalf("source replaced: %v", err)
					}
					t.Logf(
						"status=%s started_at_valid=%v source=%s steps=%d duplicate/replace rejected; append rows=0",
						got.Status,
						got.StartedAt.Valid,
						source.StepsJson,
						len(after),
					)
				},
			)
		}
	}
}

func TestDeploymentStepSnapshotRejectsInvalidStateAndRollsBack(t *testing.T) {
	r := remoteFixture(t)
	ctx := context.Background()
	for _, id := range []int64{0, -1, 9999} {
		if err := r.SnapshotDeploymentSteps(ctx, id, nil); !errors.Is(
			err,
			sql.ErrNoRows,
		) {
			t.Fatalf("invalid ID %d: %v", id, err)
		}
	}
	for _, status := range []string{"running", "succeeded", "failed", "cancelled"} {
		d, err := r.Queries.CreateDeployment(ctx, db.CreateDeploymentParams{
			ReleaseID: 1, EnvironmentID: 1, Status: status,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := r.SnapshotDeploymentSteps(ctx, d.ID, nil); !errors.Is(
			err,
			sql.ErrNoRows,
		) {
			t.Fatalf("invalid status %s: %v", status, err)
		}
	}
	d, err := r.Queries.CreateDeployment(ctx, db.CreateDeploymentParams{
		ReleaseID: 1, EnvironmentID: 1, Status: "pending_approval",
	})
	if err != nil {
		t.Fatal(err)
	}
	steps := []repository.DeploymentStepSnapshot{{
		CreateDeploymentStepParams: db.CreateDeploymentStepParams{
			Name: "approval", ExecutionTarget: "agent",
		}, Selectors: []string{""},
	}}
	if err := r.SnapshotDeploymentSteps(ctx, d.ID, steps); err == nil {
		t.Fatal("invalid selector accepted")
	}
	rows, err := r.Queries.ListDeploymentSteps(ctx, d.ID)
	if err != nil || len(rows) != 0 {
		t.Fatalf("partial steps=%v error=%v", rows, err)
	}
	if _, err := r.Queries.GetDeploymentStepSource(ctx, d.ID); !errors.Is(
		err,
		sql.ErrNoRows,
	) {
		t.Fatalf("failed snapshot sealed: %v", err)
	}
	steps[0].Selectors = []string{"linux"}
	if err := r.SnapshotDeploymentSteps(ctx, d.ID, steps); err != nil {
		t.Fatal(err)
	}
	n, err := r.Queries.CreateDeploymentStepAttempt(
		ctx,
		db.CreateDeploymentStepAttemptParams{
			DeploymentID: d.ID, StepIndex: 0, Attempt: 1, Now: 100, WaitDeadline: 400,
		},
	)
	assertZero(t, n, err)
	t.Log(
		"IDs 0/-1/9999 and terminal/running states rejected; invalid selector rolled back steps/source; fresh snapshot succeeds; approval-waiting attempt rows=0",
	)
}
