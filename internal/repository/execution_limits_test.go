package repository

import (
	"encoding/json"
	"errors"
	"math"
	"testing"

	"durpdeploy/internal/agentexecution"
	"durpdeploy/internal/db"
)

func TestDeploymentExecutionLimitsAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		r, _ := openDeploymentCreationEngine(
			t,
			newDeploymentCreationEngine(t, name),
		)
		for _, tc := range []struct {
			name             string
			timeout, retries int64
			invalid          bool
		}{
			{"default", 0, 0, false},
			{"maximum", 86400, 10, false},
			{"timeout negative", -1, 0, true},
			{"timeout excessive", 86401, 0, true},
			{"timeout overflow", math.MaxInt64, 0, true},
			{"retries negative", 0, -1, true},
			{"retries excessive", 0, 11, true},
			{"retries overflow", 0, math.MaxInt64, true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				checkDeploymentExecutionLimits(
					t,
					r,
					tc.timeout,
					tc.retries,
					tc.invalid,
				)
			})
		}
	})
}

func checkDeploymentExecutionLimits(
	t *testing.T,
	r *Repository,
	timeout, retries int64,
	invalid bool,
) {
	t.Helper()
	body, err := json.Marshal([]map[string]any{{
		"name": "bounded", "script_body": "echo bounded",
		"execution_target": "local", "container_image": "alpine:3.20",
		"timeout_seconds": timeout, "max_retries": retries,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Queries.UpdateRelease(t.Context(), db.UpdateReleaseParams{
		ID: 1, ProjectID: 1, Version: "v1", StepsJson: string(body),
	}); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := r.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM deployments").Scan(&before); err != nil {
		t.Fatal(err)
	}
	created, err := r.CreateDeployment(
		t.Context(),
		db.CreateDeploymentParams{
			ReleaseID: 1, EnvironmentID: 1, Status: "pending",
		},
	)
	if invalid {
		if !errors.Is(err, agentexecution.ErrLimits) {
			t.Fatalf("error=%v", err)
		}
		var after int
		if err := r.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM deployments").Scan(&after); err != nil {
			t.Fatal(err)
		}
		if before != after {
			t.Fatalf(
				"rejected snapshot created deployment: %d -> %d",
				before,
				after,
			)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	steps, err := r.Queries.ListDeploymentSteps(
		t.Context(),
		created.Deployment.ID,
	)
	if err != nil || len(steps) != 1 ||
		steps[0].TimeoutSeconds != timeout ||
		steps[0].MaxRetries != retries {
		t.Fatalf("steps=%+v error=%v", steps, err)
	}
	// Corrupt the frozen rerun source without changing the release.
	if _, err := r.DB.ExecContext(t.Context(), "UPDATE deployment_step_sources SET steps_json = ? WHERE deployment_id = ?", `[{"name":"bad","timeout_seconds":86401}]`, created.Deployment.ID); err != nil {
		t.Fatal(err)
	}
	_, err = r.CreateDeploymentFromDeployment(
		t.Context(),
		db.CreateDeploymentParams{
			ReleaseID:     1,
			EnvironmentID: 1,
			Status:        "pending",
		},
		created.Deployment.ID,
	)
	if !errors.Is(err, agentexecution.ErrLimits) {
		t.Fatalf("corrupt rerun error=%v", err)
	}
}
