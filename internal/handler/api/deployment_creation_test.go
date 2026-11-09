package api_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler/api"
	"durpdeploy/internal/repository"
)

func TestDeploymentRetryAndRedeployContracts(t *testing.T) {
	for _, action := range []string{"retry", "redeploy"} {
		for _, status := range []string{
			"succeeded", "failed", "cancelled", "rejected", "expired",
			"cleanup_unconfirmed", "pending", "pending_approval", "queued",
			"running", "publishing_artifact", "awaiting_artifact_approval",
		} {
			t.Run(action+"/"+status, func(t *testing.T) {
				assertDeploymentRetryAndRedeployContract(t, action, status)
			})
		}
	}
}

func assertDeploymentRetryAndRedeployContract(
	t *testing.T,
	action, status string,
) {
	t.Helper()
	h := newAPIHarness(t)
	project := seedProject(t, h.repo)
	env := seedEnv(t, h.repo)
	release := seedRelease(t, h.repo, project.ID)
	source := seedDeployment(t, h.repo, release.ID, env.ID, status)
	lifecycle, err := h.repo.Queries.CreateLifecycle(t.Context(),
		db.CreateLifecycleParams{Name: "approval"})
	require.NoError(t, err)
	_, err = h.repo.Queries.CreateLifecycleStage(t.Context(),
		db.CreateLifecycleStageParams{
			LifecycleID: lifecycle.ID, EnvironmentID: env.ID,
			RequiresApproval: 1,
		})
	require.NoError(t, err)
	require.NoError(
		t,
		h.repo.Queries.SetProjectLifecycle(t.Context(),
			db.SetProjectLifecycleParams{
				ID: project.ID,
				LifecycleID: sql.NullInt64{
					Int64: lifecycle.ID,
					Valid: true,
				},
			}),
	)
	request := withAPIURLParam(httptest.NewRequest(
		http.MethodPost, "/", nil), "id", fmt.Sprint(source.ID))
	recorder := httptest.NewRecorder()
	handler := api.NewDeploymentHandler(h.repo, h.runner)
	call := handler.RetryDeployment
	wantNote := sql.NullString{
		String: fmt.Sprintf("Retry of #%d", source.ID), Valid: true,
	}
	if action == "redeploy" {
		call = handler.RedeployDeployment
		wantNote = sql.NullString{}
	}
	call(recorder, request)

	wantStatus := http.StatusCreated
	wantError := ""
	switch status {
	case "failed", "cancelled", "rejected", "expired":
	case "cleanup_unconfirmed":
		wantStatus = http.StatusConflict
		wantError = repository.ErrContainerCleanupUnconfirmed.Error()
	default:
		if status != "succeeded" || action == "retry" {
			wantStatus = http.StatusBadRequest
			wantError = "Can only retry failed or cancelled deployments"
			if action == "redeploy" {
				wantStatus = http.StatusConflict
				wantError = "Can only redeploy terminal deployments"
			}
		}
	}
	require.Equal(
		t,
		wantStatus,
		recorder.Code,
		recorder.Body.String(),
	)
	if wantError != "" {
		var response struct{ Error string }
		mustDecode(t, recorder.Body, &response)
		require.Equal(t, wantError, response.Error)
	} else {
		var next db.Deployment
		mustDecode(t, recorder.Body, &next)
		require.Equal(t, "pending_approval", next.Status)
		require.False(t, next.StartedAt.Valid)
		require.Equal(t, wantNote, next.Note)
	}
	var count int
	require.NoError(t, h.repo.DB.QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM deployments").Scan(&count))
	wantCount := 1
	if wantStatus == http.StatusCreated {
		wantCount++
	}
	require.Equal(t, wantCount, count)
}

func TestDeploymentRetryAndRedeployCreationFailures(t *testing.T) {
	for _, action := range []string{"retry", "redeploy"} {
		for _, failure := range []struct {
			name, statement, message string
			status                   int
		}{
			{"missing at lock", `CREATE TRIGGER disappear_at_release_lock
BEFORE UPDATE OF version ON releases BEGIN SELECT RAISE(IGNORE); END`,
				"create deployment from deployment: " + repository.ErrDeploymentParent.Error(), http.StatusConflict},
			{"insert failure", `CREATE TRIGGER reject_deployment
BEFORE INSERT ON deployments BEGIN SELECT RAISE(ABORT,'injected failure'); END`,
				"injected failure", http.StatusInternalServerError},
			{"legacy step", `UPDATE releases SET steps_json=
'[{"name":"legacy","script_body":"true","execution_target":"local"}]'`,
				repository.ErrLegacyServerStep.Error(), http.StatusConflict},
		} {
			t.Run(action+"/"+failure.name, func(t *testing.T) {
				h := newAPIHarness(t)
				project := seedProject(t, h.repo)
				env := seedEnv(t, h.repo)
				release := seedRelease(t, h.repo, project.ID)
				source := seedDeployment(
					t,
					h.repo,
					release.ID,
					env.ID,
					"failed",
				)
				_, err := h.repo.DB.ExecContext(t.Context(), failure.statement)
				require.NoError(t, err)
				request := withAPIURLParam(httptest.NewRequest(
					http.MethodPost, "/", nil), "id", fmt.Sprint(source.ID))
				recorder := httptest.NewRecorder()
				handler := api.NewDeploymentHandler(h.repo, h.runner)
				if action == "retry" {
					handler.RetryDeployment(recorder, request)
				} else {
					handler.RedeployDeployment(recorder, request)
				}
				require.Equal(
					t,
					failure.status,
					recorder.Code,
					recorder.Body.String(),
				)
				var response struct{ Error string }
				mustDecode(t, recorder.Body, &response)
				if failure.status == http.StatusInternalServerError {
					require.Contains(t, response.Error, failure.message)
				} else {
					require.Equal(t, failure.message, response.Error)
				}
				var count int
				require.NoError(t, h.repo.DB.QueryRowContext(t.Context(),
					"SELECT COUNT(*) FROM deployments").Scan(&count))
				require.Equal(t, 1, count)
				stored, err := h.repo.Queries.GetDeployment(
					t.Context(),
					source.ID,
				)
				require.NoError(t, err)
				require.Equal(t, "failed", stored.Status)
			})
		}
	}
}

func TestRetryDeploymentCreatesLocalSnapshot(t *testing.T) {
	harness := newAPIHarness(t)
	user := seedAPIUser(t, harness.repo, "retry@example.com", "admin")
	project := seedProject(t, harness.repo)
	environment := seedEnv(t, harness.repo)
	release := seedRelease(t, harness.repo, project.ID)
	const mixedSteps = `[` +
		`{"name":"bash-step","script_body":"echo hi",` +
		`"interpreter":"bash","execution_target":"local","container_image":"harbor.durp.info/dockerhub/library/alpine:3.20"},` +
		`{"name":"python-step","script_body":"print('ok')",` +
		`"interpreter":"python3","execution_target":"local","container_image":"harbor.durp.info/dockerhub/library/python:3.12"}]`
	if _, err := harness.repo.Queries.UpdateRelease(
		context.Background(),
		db.UpdateReleaseParams{
			ID: release.ID, ProjectID: project.ID,
			Version: release.Version, StepsJson: mixedSteps,
		},
	); err != nil {
		t.Fatal(err)
	}
	sourceResult, err := harness.repo.CreateDeployment(
		context.Background(),
		db.CreateDeploymentParams{
			ReleaseID: release.ID, EnvironmentID: environment.ID,
			Status: "pending",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := harness.repo.Queries.UpdateDeploymentStatus(
		context.Background(),
		db.UpdateDeploymentStatusParams{
			ID: sourceResult.Deployment.ID, Status: "failed",
		},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.repo.Queries.CreateDeploymentLog(
		context.Background(),
		db.CreateDeploymentLogParams{
			DeploymentID: sourceResult.Deployment.ID,
			Line:         "source-only log",
		},
	); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/api/v1/deployments/%d/retry", sourceResult.Deployment.ID),
		nil,
	)
	request = withAPIUser(request, user)
	request = withAPIURLParam(
		request,
		"id",
		fmt.Sprint(sourceResult.Deployment.ID),
	)
	recorder := httptest.NewRecorder()

	api.NewDeploymentHandler(harness.repo, harness.runner).
		RetryDeployment(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf(
			"retry status=%d body=%s",
			recorder.Code,
			recorder.Body.String(),
		)
	}
	var retried db.Deployment
	mustDecode(t, recorder.Body, &retried)
	if retried.ID == sourceResult.Deployment.ID ||
		retried.ReleaseID != release.ID ||
		retried.EnvironmentID != environment.ID ||
		retried.AssignedAgentID.Valid {
		t.Fatalf(
			"retry deployment=%+v source=%+v",
			retried,
			sourceResult.Deployment,
		)
	}
	source, err := harness.repo.Queries.GetDeployment(
		context.Background(),
		sourceResult.Deployment.ID,
	)
	if err != nil || source.Status != "failed" {
		t.Fatalf("source status=%q error=%v", source.Status, err)
	}
	logs, err := harness.repo.Queries.ListDeploymentLogsByDeployment(
		context.Background(),
		source.ID,
	)
	if err != nil || len(logs) != 1 || logs[0].Line != "source-only log" {
		t.Fatalf("source logs=%v error=%v", logs, err)
	}
	if _, err := harness.repo.Queries.GetDeploymentStepSource(
		context.Background(),
		retried.ID,
	); err != nil {
		t.Fatalf("retry source snapshot: %v", err)
	}
	retriedSteps, err := harness.repo.Queries.ListDeploymentSteps(
		context.Background(),
		retried.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	wantInterpreters := map[string]string{
		"bash-step": "bash", "python-step": "python3",
	}
	if len(retriedSteps) != len(wantInterpreters) {
		t.Fatalf("retry steps = %+v", retriedSteps)
	}
	for _, step := range retriedSteps {
		if want := wantInterpreters[step.Name]; step.Interpreter != want {
			t.Fatalf(
				"step %q interpreter=%q want %q",
				step.Name, step.Interpreter, want,
			)
		}
	}
}

func TestDeploymentRetryAndRedeployRejectUnconfirmedCleanup(t *testing.T) {
	// Given
	harness := newAPIHarness(t)
	project := seedProject(t, harness.repo)
	environment := seedEnv(t, harness.repo)
	release := seedRelease(t, harness.repo, project.ID)
	source, err := harness.repo.Queries.CreateDeployment(
		t.Context(),
		db.CreateDeploymentParams{
			ReleaseID: release.ID, EnvironmentID: environment.ID,
			Status: "cleanup_unconfirmed",
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	for _, action := range []struct {
		name string
		call func(http.ResponseWriter, *http.Request)
	}{
		{"retry", api.NewDeploymentHandler(harness.repo, harness.runner).RetryDeployment},
		{"redeploy", api.NewDeploymentHandler(harness.repo, harness.runner).RedeployDeployment},
	} {
		t.Run(action.name, func(t *testing.T) {
			// When
			request := withAPIURLParam(httptest.NewRequest(
				http.MethodPost,
				fmt.Sprintf(
					"/api/v1/deployments/%d/%s",
					source.ID,
					action.name,
				),
				nil,
			),
				"id", fmt.Sprint(source.ID))
			recorder := httptest.NewRecorder()
			action.call(recorder, request)

			// Then
			if recorder.Code != http.StatusConflict {
				t.Fatalf(
					"status=%d body=%s",
					recorder.Code,
					recorder.Body.String(),
				)
			}
		})
	}
}
