//go:build e2e

package api_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"durpdeploy/internal/config"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func limitsStep(timeout, retries int64) map[string]any {
	return map[string]any{
		"name":             "bounded",
		"script_body":      "echo bounded",
		"execution_target": "local",
		"container_image":  "harbor.durp.info/dockerhub/library/bash:5.2",
		"timeout_seconds":  timeout,
		"max_retries":      retries,
	}
}

func limitsForm(timeout, retries int64, runbook bool) url.Values {
	if runbook {
		return url.Values{
			"name":             {"bounded runbook"},
			"description":      {"unchanged"},
			"step_name":        {"bounded"},
			"step_script":      {"echo bounded"},
			"step_interpreter": {"bash"},
			"step_timeout":     {strconv.FormatInt(timeout, 10)},
			"step_retries":     {strconv.FormatInt(retries, 10)},
			"step_target":      {"local"},
			"step_selectors":   {""},
			"step_image":       {"harbor.durp.info/dockerhub/library/bash:5.2"},
		}
	}
	return url.Values{
		"name":             {"bounded"},
		"script_body":      {"echo bounded"},
		"interpreter":      {"bash"},
		"execution_target": {"local"},
		"container_image":  {"harbor.durp.info/dockerhub/library/bash:5.2"},
		"timeout_seconds":  {strconv.FormatInt(timeout, 10)},
		"max_retries":      {strconv.FormatInt(retries, 10)},
	}
}

func TestStepExecutionLimitsAPIWebE2E(t *testing.T) {
	f := newArtifactE2E(t)
	checkStepExecutionLimitsAPIWeb(t, f, config.Defaults().Limits.Execution)
}

func newExecutionLimitsFixture(
	t *testing.T,
	retries, timeout int64,
) *artifactE2E {
	t.Helper()
	settings := config.Defaults()
	settings.Container.Runtime = os.Getenv("DURPDEPLOY_CONTAINER_RUNTIME")
	settings.Container.Namespace = "execution-limits-" + uuid.NewString()
	settings.Limits.Execution.MaxRetries = retries
	settings.Limits.Execution.MaxTimeoutSeconds = timeout
	settings.Limits.Execution.DefaultTimeoutSeconds = min(300, timeout)
	f := newArtifactPolicyFixture(t, settings)
	f.client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	f.api(t, "DELETE", f.base()+"/package-repository", nil, 204)
	f.environment = seedEnv(t, f.h.repo)
	settings.Limits.Execution.MaxRetries = 99
	t.Setenv("DURPDEPLOY_LIMITS_EXECUTION_MAX_RETRIES", "99")
	t.Setenv("DURPDEPLOY_LIMITS_EXECUTION_MAX_TIMEOUT_SECONDS", "999999")
	return f
}

func TestConfiguredExecutionLimitsAPIWebE2E(t *testing.T) {
	for _, limits := range []struct{ retries, timeout int64 }{{0, 3}, {2, 600}, {11, 172800}} {
		t.Run(
			fmt.Sprintf("%d/%d", limits.retries, limits.timeout),
			func(t *testing.T) {
				f := newExecutionLimitsFixture(
					t,
					limits.retries,
					limits.timeout,
				)
				checkStepExecutionLimitsAPIWeb(
					t,
					f,
					f.h.repo.ResourceLimits().Execution,
				)
			},
		)
	}
}

func checkStepExecutionLimitsAPIWeb(
	t *testing.T,
	f *artifactE2E,
	limits config.ExecutionLimits,
) {
	t.Helper()
	var captured config.Limits
	decodeLifecycleTest(
		t,
		f.api(t, "GET", "/api/v1/admin/resource-config", nil, 200),
		&captured,
	)
	require.Equal(t, limits, captured.Execution)
	var step struct{ ID int64 }
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"POST",
			f.base()+"/steps",
			limitsStep(limits.MaxTimeoutSeconds, limits.MaxRetries),
			201,
		),
		&step,
	)
	var book struct {
		Runbook db.Runbook
		Version db.RunbookVersion
	}
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"POST",
			f.base()+"/runbooks",
			map[string]any{
				"name":        "bounded runbook",
				"description": "unchanged",
				"steps": []any{
					limitsStep(limits.MaxTimeoutSeconds, limits.MaxRetries),
				},
			},
			201,
		),
		&book,
	)
	stepPath := fmt.Sprintf("%s/steps/%d", f.base(), step.ID)
	bookPath := fmt.Sprintf("%s/runbooks/%d", f.base(), book.Runbook.ID)
	beforeStep := string(f.api(t, "GET", stepPath, nil, 200))
	beforeBook := string(f.api(t, "GET", bookPath, nil, 200))
	var release db.Release
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"POST",
			f.base()+"/releases",
			map[string]string{"version": "limits"},
			201,
		),
		&release,
	)
	var deployment db.Deployment
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"POST",
			f.base()+"/deployments",
			map[string]int64{
				"release_id":     release.ID,
				"environment_id": f.environment.ID,
			},
			201,
		),
		&deployment,
	)
	waitVerificationStatus(t, f, deployment.ID, "succeeded")
	stored, err := f.h.repo.Queries.ListDeploymentSteps(
		t.Context(),
		deployment.ID,
	)
	if err != nil || len(stored) != 1 ||
		stored[0].TimeoutSeconds != limits.MaxTimeoutSeconds ||
		stored[0].MaxRetries != limits.MaxRetries {
		t.Fatalf("maximum snapshot=%+v: %v", stored, err)
	}
	var execution struct {
		ID           int64
		DeploymentID int64 `json:"deployment_id"`
	}
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"POST",
			bookPath+"/executions",
			map[string]int64{
				"environment_id": f.environment.ID,
				"version_id":     book.Version.ID,
			},
			201,
		),
		&execution,
	)
	waitExecutionStatus(
		t,
		f,
		fmt.Sprintf("%s/runbook-executions/%d", f.base(), execution.ID),
		"succeeded",
	)
	for _, tc := range []struct {
		name             string
		timeout, retries int64
	}{
		{"timeout negative", -1, 0}, {"timeout excessive", limits.MaxTimeoutSeconds + 1, 0}, {"timeout overflow", math.MaxInt64, 0},
		{"retries negative", 0, -1}, {"retries excessive", 0, limits.MaxRetries + 1}, {"retries overflow", 0, math.MaxInt64},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.checkExecutionLimitRejection(
				t,
				tc.timeout,
				tc.retries,
				stepPath,
				bookPath,
				beforeStep,
				beforeBook,
			)
		})
	}
	// Zero values keep their existing meanings and remain writable through both surfaces.
	f.api(t, "PUT", stepPath, limitsStep(0, 0), 200)
	f.web(
		t,
		"PUT",
		fmt.Sprintf("/projects/%d/steps/%d", f.project.ID, step.ID),
		limitsForm(limits.MaxTimeoutSeconds, limits.MaxRetries, false),
		200,
	)
	f.api(
		t,
		"PUT",
		bookPath,
		map[string]any{"steps": []any{limitsStep(0, 0)}},
		201,
	)
	f.web(
		t,
		"POST",
		fmt.Sprintf(
			"/projects/%d/runbooks/%d/save",
			f.project.ID,
			book.Runbook.ID,
		),
		limitsForm(limits.MaxTimeoutSeconds, limits.MaxRetries, true),
		303,
	)
	// Execution entry points reject a corrupted persisted release before writes.
	scheduleBody := map[string]any{
		"release_id":     release.ID,
		"environment_id": f.environment.ID,
		"cron":           "0 0 * * *",
		"enabled":        false,
	}
	var schedule struct{ ID int64 }
	decodeLifecycleTest(
		t,
		f.api(t, "POST", f.base()+"/schedules", scheduleBody, 201),
		&schedule,
	)
	schedulePath := fmt.Sprintf("%s/schedules/%d", f.base(), schedule.ID)
	beforeSchedule := string(f.api(t, "GET", schedulePath, nil, 200))
	if _, err := f.h.repo.DB.ExecContext(t.Context(), "UPDATE steps SET max_retries=? WHERE id=?", limits.MaxRetries+1, step.ID); err != nil {
		t.Fatal(err)
	}
	f.api(
		t,
		"POST",
		f.base()+"/releases",
		map[string]string{"version": "rejected"},
		422,
	)
	f.web(
		t,
		"POST",
		fmt.Sprintf("/projects/%d/releases", f.project.ID),
		url.Values{"version": {"rejected-web"}},
		422,
	)
	if _, err := f.h.repo.DB.ExecContext(t.Context(), "UPDATE releases SET steps_json=? WHERE id=?", `[{"name":"bad","max_retries":9223372036854775807}]`, release.ID); err != nil {
		t.Fatal(err)
	}
	f.api(
		t,
		"POST",
		f.base()+"/deployments",
		map[string]int64{
			"release_id":     release.ID,
			"environment_id": f.environment.ID,
		},
		422,
	)
	f.api(t, "POST", f.base()+"/schedules", scheduleBody, 422)
	f.api(t, "PUT", schedulePath, scheduleBody, 422)
	f.api(t, "POST", schedulePath+"/toggle", nil, 422)
	webBase := fmt.Sprintf("/projects/%d", f.project.ID)
	webSchedule := fmt.Sprintf("%s/schedules/%d", webBase, schedule.ID)
	form := url.Values{
		"release_id":     {fmt.Sprint(release.ID)},
		"environment_id": {fmt.Sprint(f.environment.ID)},
		"cron_expr":      {"0 0 * * *"},
	}
	f.web(t, "POST", webBase+"/schedules", form, 422)
	f.web(t, "PUT", webSchedule, form, 422)
	f.web(t, "POST", webSchedule+"/toggle", nil, 422)
	if got := string(f.api(t, "GET", schedulePath, nil, 200)); got != beforeSchedule {
		t.Fatalf("invalid snapshot changed schedule: %s", got)
	}
	if _, err := f.h.repo.DB.ExecContext(t.Context(), "UPDATE releases SET steps_json=? WHERE id=?", fmt.Sprintf(`[{"name":"bad","timeout_seconds":%d}]`, limits.MaxTimeoutSeconds+1), book.Version.ReleaseID); err != nil {
		t.Fatal(err)
	}
	request := map[string]int64{
		"environment_id": f.environment.ID,
		"version_id":     book.Version.ID,
	}
	f.api(t, "POST", bookPath+"/executions", request, 422)
	f.api(t, "POST", bookPath+"/schedules", request, 422)
	bookForm := url.Values{
		"environment_id": {fmt.Sprint(f.environment.ID)},
		"version_id":     {fmt.Sprint(book.Version.ID)},
		"cron":           {"0 0 * * *"},
	}
	webBook := fmt.Sprintf("%s/runbooks/%d", webBase, book.Runbook.ID)
	f.web(t, "POST", webBook+"/execute", bookForm, 422)
	f.web(t, "POST", webBook+"/schedules", bookForm, 422)
	if _, err := f.h.repo.DB.ExecContext(t.Context(), "UPDATE deployment_step_sources SET steps_json=? WHERE deployment_id=?", fmt.Sprintf(`[{"name":"bad","max_retries":%d}]`, limits.MaxRetries+1), execution.DeploymentID); err != nil {
		t.Fatal(err)
	}
	f.api(
		t,
		"POST",
		fmt.Sprintf("%s/runbook-executions/%d/retry", f.base(), execution.ID),
		nil,
		422,
	)
	f.web(
		t,
		"POST",
		fmt.Sprintf("%s/runbooks/executions/%d/retry", webBase, execution.ID),
		nil,
		422,
	)
	checkExecutionLimitsTemplate(t, f, stepPath, limits)
	checkExecutionLimitsRefresh(
		t,
		f,
		stepPath,
		release.ID,
		deployment.ID,
		limits,
	)
}

func checkExecutionLimitsRefresh(
	t *testing.T,
	f *artifactE2E,
	stepPath string,
	releaseID, deploymentID int64,
	limits config.ExecutionLimits,
) {
	t.Helper()
	before, err := f.h.repo.Queries.ListDeploymentSteps(
		t.Context(),
		deploymentID,
	)
	require.NoError(t, err)
	sourceBefore, err := f.h.repo.Queries.GetDeploymentStepSource(
		t.Context(),
		deploymentID,
	)
	require.NoError(t, err)
	path := fmt.Sprintf("%s/releases/%d", f.base(), releaseID)
	for _, tc := range []struct {
		web              bool
		timeout, retries int64
	}{
		{false, limits.MaxTimeoutSeconds + 1, limits.MaxRetries},
		{false, limits.MaxTimeoutSeconds, limits.MaxRetries + 1},
		{true, limits.MaxTimeoutSeconds + 1, limits.MaxRetries},
		{true, limits.MaxTimeoutSeconds, limits.MaxRetries + 1},
	} {
		old := limitsStep(tc.timeout, tc.retries)
		encoded, err := json.Marshal([]any{old})
		require.NoError(t, err)
		_, err = f.h.repo.DB.ExecContext(
			t.Context(),
			"UPDATE releases SET steps_json=? WHERE id=?",
			string(encoded),
			releaseID,
		)
		require.NoError(t, err)
		f.api(
			t,
			"POST",
			f.base()+"/deployments",
			map[string]int64{
				"release_id":     releaseID,
				"environment_id": f.environment.ID,
			},
			422,
		)
		if !tc.web {
			f.api(t, "POST", path+"/refresh", nil, 200)
		} else {
			f.web(t, "POST", strings.TrimPrefix(path, "/api/v1")+"/refresh", nil, 303)
		}
		var refreshed db.Release
		decodeLifecycleTest(t, f.api(t, "GET", path, nil, 200), &refreshed)
		var steps []struct {
			TimeoutSeconds int64 `json:"timeout_seconds"`
			MaxRetries     int64 `json:"max_retries"`
		}
		require.NoError(t, json.Unmarshal([]byte(refreshed.StepsJson), &steps))
		require.Len(t, steps, 1)
		require.Equal(t, limits.MaxTimeoutSeconds, steps[0].TimeoutSeconds)
		require.Equal(t, limits.MaxRetries, steps[0].MaxRetries)
		after, err := f.h.repo.Queries.ListDeploymentSteps(
			t.Context(),
			deploymentID,
		)
		require.NoError(t, err)
		require.Equal(t, before, after)
		sourceAfter, err := f.h.repo.Queries.GetDeploymentStepSource(
			t.Context(),
			deploymentID,
		)
		require.NoError(t, err)
		require.Equal(t, sourceBefore, sourceAfter)
	}
	var current db.Release
	decodeLifecycleTest(t, f.api(t, "GET", path, nil, 200), &current)
	_, err = f.h.repo.DB.ExecContext(
		t.Context(),
		"UPDATE steps SET max_retries=? WHERE project_id=?",
		limits.MaxRetries+1,
		f.project.ID,
	)
	require.NoError(t, err)
	f.api(t, "POST", path+"/refresh", nil, 422)
	var rejected db.Release
	decodeLifecycleTest(t, f.api(t, "GET", path, nil, 200), &rejected)
	require.Equal(t, current, rejected)
	f.api(
		t,
		"PUT",
		stepPath,
		limitsStep(limits.MaxTimeoutSeconds, limits.MaxRetries),
		200,
	)
}

func checkExecutionLimitsTemplate(
	t *testing.T,
	f *artifactE2E,
	stepPath string,
	limits config.ExecutionLimits,
) {
	t.Helper()
	f.api(
		t,
		"PUT",
		stepPath,
		limitsStep(limits.MaxTimeoutSeconds, limits.MaxRetries),
		200,
	)
	var saved struct {
		ID       int64
		Snapshot repository.ProjectTemplateSnapshot
	}
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"POST",
			f.base()+"/project-templates",
			map[string]string{"name": "Execution bounds"},
			201,
		),
		&saved,
	)
	path := fmt.Sprintf("%s/project-templates/%d/projects", f.base(), saved.ID)
	f.api(
		t,
		"POST",
		path,
		repository.ApplyProjectTemplate{
			Name:     "Valid execution bounds",
			Reviewed: true,
		},
		201,
	)
	before, err := f.h.repo.Queries.CountProjects(t.Context())
	require.NoError(t, err)
	for _, field := range []string{"timeout", "retries"} {
		snapshot := saved.Snapshot
		snapshot.Steps = append(
			[]repository.ProjectTemplateStep(nil),
			saved.Snapshot.Steps...)
		if field == "timeout" {
			snapshot.Steps[0].TimeoutSeconds = limits.MaxTimeoutSeconds + 1
		} else {
			snapshot.Steps[0].MaxRetries = limits.MaxRetries + 1
		}
		encoded, err := json.Marshal(snapshot)
		require.NoError(t, err)
		encrypted, err := f.h.repo.EncryptValue(
			sql.NullString{String: string(encoded), Valid: true},
		)
		require.NoError(t, err)
		_, err = f.h.repo.DB.ExecContext(
			t.Context(),
			"UPDATE project_templates SET snapshot_json=? WHERE id=?",
			encrypted.String,
			saved.ID,
		)
		require.NoError(t, err)
		f.api(
			t,
			"POST",
			path,
			repository.ApplyProjectTemplate{
				Name:     "Rejected execution bounds",
				Reviewed: true,
			},
			422,
		)
		after, err := f.h.repo.Queries.CountProjects(t.Context())
		require.NoError(t, err)
		require.Equal(t, before, after)
	}
}

func (f *artifactE2E) checkExecutionLimitRejection(
	t *testing.T,
	timeout, retries int64,
	stepPath, bookPath, beforeStep, beforeBook string,
) {
	t.Helper()
	body := limitsStep(timeout, retries)
	f.api(t, "POST", f.base()+"/steps", body, 400)
	f.api(t, "PUT", stepPath, body, 400)
	request := map[string]any{
		"name":        "changed",
		"description": "changed",
		"steps":       []any{limitsStep(0, 0), body},
	}
	f.api(t, "POST", f.base()+"/runbooks", request, 422)
	f.api(t, "PUT", bookPath, request, 422)
	webBase := fmt.Sprintf("/projects/%d", f.project.ID)
	f.web(
		t,
		"POST",
		webBase+"/steps",
		limitsForm(timeout, retries, false),
		422,
	)
	f.web(
		t,
		"PUT",
		strings.TrimPrefix(stepPath, "/api/v1"),
		limitsForm(timeout, retries, false),
		422,
	)
	f.web(
		t,
		"POST",
		webBase+"/runbooks",
		limitsForm(timeout, retries, true),
		422,
	)
	f.web(
		t,
		"POST",
		strings.TrimPrefix(bookPath, "/api/v1")+"/save",
		limitsForm(timeout, retries, true),
		422,
	)
	if got := string(f.api(t, "GET", stepPath, nil, 200)); got != beforeStep {
		t.Fatalf("rejected update changed step: %s", got)
	}
	if got := string(f.api(t, "GET", bookPath, nil, 200)); got != beforeBook {
		t.Fatalf("rejected update changed runbook: %s", got)
	}
	for table, want := range map[string]int{"steps": 1, "runbooks": 1, "runbook_versions": 1, "releases": 2} {
		var count int
		if err := f.h.repo.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil ||
			count != want {
			t.Fatalf("%s count=%d want=%d: %v", table, count, want, err)
		}
	}
}
