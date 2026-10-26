package runner

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"

	"durpdeploy/internal/agentexecution"
	"durpdeploy/internal/config"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func TestRunnerRejectsInvalidExecutionLimitsBeforeWork(t *testing.T) {
	for _, step := range []deploymentStep{
		{MaxRetries: 11}, {MaxRetries: math.MaxInt64},
		{TimeoutSeconds: 86401}, {TimeoutSeconds: math.MaxInt64},
	} {
		t.Run(
			fmt.Sprintf("%d/%d", step.TimeoutSeconds, step.MaxRetries),
			func(t *testing.T) {
				// A missing repository/runtime/log writer proves rejection precedes work.
				r := &DeploymentRunner{}
				if err := r.runStepAttempt(t.Context(), localStepAttempt{step: step}); !errors.Is(
					err,
					agentexecution.ErrLimits,
				) {
					t.Fatalf("local error=%v", err)
				}
				if err := r.runRemoteStep(t.Context(), t.Context(), remoteStepRequest{step: step}); !errors.Is(
					err,
					agentexecution.ErrLimits,
				) {
					t.Fatalf("remote error=%v", err)
				}
			},
		)
	}
}

func TestRunnerValidatesAllFrozenStepsAndCountsRetries(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		retries, timeout       int64
		invalid                bool
		exit                   int
		wantAttempts           int
		wantStatus             string
		maxRetries, maxTimeout int64
	}{
		{"zero", 0, 0, false, 0, 1, "succeeded", 10, 86400},
		{"maximum", 10, 86400, false, 1, 11, "failed", 10, 86400},
		{"retries overflow", math.MaxInt64, 0, true, 0, 0, "failed", 10, 86400},
		{"timeout overflow", 0, math.MaxInt64, true, 0, 0, "failed", 10, 86400},
		{"no retries", 0, 3, false, 1, 1, "failed", 0, 3},
		{"lower maximum", 2, 3, false, 1, 3, "failed", 2, 3},
		{"raised maximum", 11, 172800, false, 1, 12, "failed", 11, 172800},
		{"lowered retries snapshot", 3, 0, true, 0, 0, "failed", 2, 600},
		{"lowered timeout snapshot", 0, 4, true, 0, 0, "failed", 2, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, repo, trace := podmanFixture(t, fmt.Sprintf(`
case "$3" in
info) printf '{"host":{"security":{"rootless":true}}}';;
ps) ;;
run) cat >/dev/null; printf 'attempt\n' >> "$PODMAN_TRACE"; exit %d;;
esac
`, tc.exit))
			settings, err := config.Load()
			if err != nil {
				t.Fatal(err)
			}
			settings.Limits.Execution = config.ExecutionLimits{
				MaxRetries: tc.maxRetries, MaxTimeoutSeconds: tc.maxTimeout,
				DefaultTimeoutSeconds: min(300, tc.maxTimeout),
			}
			repo = repository.NewConfigured(repo.DB, settings)
			r.repo = repo
			t.Setenv("DURPDEPLOY_LIMITS_EXECUTION_MAX_RETRIES", "99")
			t.Setenv(
				"DURPDEPLOY_LIMITS_EXECUTION_MAX_TIMEOUT_SECONDS",
				"999999",
			)
			project, err := repo.Queries.CreateProject(
				t.Context(),
				db.CreateProjectParams{Name: "limits"},
			)
			if err != nil {
				t.Fatal(err)
			}
			env, err := repo.Queries.CreateEnvironment(
				t.Context(),
				db.CreateEnvironmentParams{Name: "limits"},
			)
			if err != nil {
				t.Fatal(err)
			}
			release, err := repo.Queries.CreateRelease(
				t.Context(),
				db.CreateReleaseParams{
					ProjectID: project.ID,
					Version:   "limits",
					StepsJson: `[{"name":"bounded","script_body":"echo bounded","container_image":"alpine:3.20"}]`,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			created, err := repo.CreateDeployment(
				t.Context(),
				db.CreateDeploymentParams{
					ReleaseID:     release.ID,
					EnvironmentID: env.ID,
					Status:        "pending",
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			steps := []deploymentStep{
				{
					Name:            "bounded",
					ScriptBody:      "echo bounded",
					ContainerImage:  "alpine:3.20",
					ExecutionTarget: "local",
					MaxRetries:      tc.retries,
					TimeoutSeconds:  tc.timeout,
				},
			}
			if tc.invalid {
				steps = append(
					[]deploymentStep{
						{
							Name:            "first",
							ScriptBody:      "echo never",
							ContainerImage:  "alpine:3.20",
							ExecutionTarget: "local",
						},
					},
					steps...)
			}
			body, err := json.Marshal(steps)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := repo.DB.ExecContext(t.Context(), "UPDATE deployment_step_sources SET steps_json=? WHERE deployment_id=?", string(body), created.Deployment.ID); err != nil {
				t.Fatal(err)
			}
			r.Run(t.Context(), created.Deployment.ID, release.ID, env.ID)
			stored, err := repo.Queries.GetDeployment(
				t.Context(),
				created.Deployment.ID,
			)
			if err != nil || stored.Status != tc.wantStatus ||
				!stored.FinishedAt.Valid {
				t.Fatalf("deployment=%+v: %v", stored, err)
			}
			data, err := os.ReadFile(trace)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
			if got := strings.Count(string(data), "attempt\n"); got != tc.wantAttempts {
				t.Fatalf(
					"attempts=%d want=%d trace=%s",
					got,
					tc.wantAttempts,
					data,
				)
			}
		})
	}
}
