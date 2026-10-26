//go:build e2e

package api_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"durpdeploy/internal/config"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"github.com/stretchr/testify/require"
)

func configuredLogLimits(raised bool) config.LogLimits {
	if raised {
		return config.LogLimits{PageBytes: 2 << 20, LineBytes: 32768,
			PreviewBatch: 65, PendingBytes: 1048577, ExportBatch: 257, PageRows: 501}
	}
	return config.LogLimits{PageBytes: 8192, LineBytes: 256,
		PreviewBatch: 2, PendingBytes: 128, ExportBatch: 3, PageRows: 4}
}

func newConfiguredLogE2E(t *testing.T, limits config.LogLimits) *artifactE2E {
	t.Helper()
	for field, value := range map[string]int64{
		"PAGE_BYTES": limits.PageBytes, "LINE_BYTES": limits.LineBytes,
		"PREVIEW_BATCH": limits.PreviewBatch, "PENDING_BYTES": limits.PendingBytes,
		"EXPORT_BATCH": limits.ExportBatch, "PAGE_ROWS": limits.PageRows,
	} {
		t.Setenv("DURPDEPLOY_LIMITS_LOGS_"+field, strconv.FormatInt(value, 10))
	}
	container := config.Defaults().Limits.Container
	return newConfiguredArtifactE2E(t, &container)
}

func TestConfiguredLogsAPIWebE2E(t *testing.T) {
	for _, raised := range []bool{false, true} {
		t.Run(fmt.Sprint(raised), func(t *testing.T) {
			limits := configuredLogLimits(raised)
			f := newConfiguredLogE2E(t, limits)
			for _, runbook := range []bool{false, true} {
				t.Run(fmt.Sprint(runbook), func(t *testing.T) {
					testConfiguredLogEndpoints(t, f, limits, runbook)
				})
			}
		})
	}
}

func configuredLogTarget(
	t *testing.T,
	f *artifactE2E,
	runbook bool,
) logHistoryTarget {
	t.Helper()
	if runbook {
		book, _, err := f.h.repo.SaveRunbook(
			t.Context(),
			repository.RunbookSave{
				ProjectID: f.project.ID, Name: "Configured logs", StepsJSON: "[]",
			},
		)
		require.NoError(t, err)
		execution, result, err := f.h.repo.CreateRunbookExecution(
			t.Context(),
			repository.RunbookExecutionRequest{
				ProjectID: f.project.ID, RunbookID: book.ID, EnvironmentID: f.environment.ID,
			},
		)
		require.NoError(t, err)
		require.NoError(
			t,
			f.h.repo.Queries.UpdateDeploymentStatus(
				t.Context(),
				db.UpdateDeploymentStatusParams{
					ID: result.Deployment.ID, Status: "succeeded",
				},
			),
		)
		return logHistoryTarget{DeploymentID: result.Deployment.ID,
			APIPath: fmt.Sprintf(
				"%s/runbook-executions/%d/logs",
				f.base(),
				execution.ID,
			),
			WebPath: fmt.Sprintf(
				"/projects/%d/runbooks/executions/%d",
				f.project.ID,
				execution.ID,
			),
			Component: "deploymentStream"}
	}
	release := seedRelease(t, f.h.repo, f.project.ID)
	deployment := seedDeployment(
		t,
		f.h.repo,
		release.ID,
		f.environment.ID,
		"succeeded",
	)
	return logHistoryTarget{DeploymentID: deployment.ID,
		APIPath: fmt.Sprintf("/api/v1/deployments/%d/logs", deployment.ID),
		WebPath: fmt.Sprintf(
			"/deployments/%d",
			deployment.ID,
		), Component: "deploymentStepLogs"}
}

func seedConfiguredLogRows(
	t *testing.T,
	f *artifactE2E,
	deploymentID int64,
	limits config.LogLimits,
) string {
	t.Helper()
	line := strings.Repeat(
		"界😀\t<&",
		int(limits.LineBytes)/4,
	) + "complete-configured-tail"
	require.NoError(t, f.h.repo.WithTx(t.Context(), func(q *db.Queries) error {
		for index := range 520 {
			_, err := q.CreateDeploymentLog(
				t.Context(),
				db.CreateDeploymentLogParams{
					DeploymentID: deploymentID, Line: fmt.Sprintf("configured-row-%03d", index),
				},
			)
			if err != nil {
				return err
			}
		}
		_, err := q.CreateDeploymentLog(
			t.Context(),
			db.CreateDeploymentLogParams{
				DeploymentID: deploymentID, Line: line,
			},
		)
		return err
	}))
	return line
}

func testConfiguredLogEndpoints(
	t *testing.T,
	f *artifactE2E,
	limits config.LogLimits,
	runbook bool,
) {
	t.Helper()
	target := configuredLogTarget(t, f, runbook)
	line := seedConfiguredLogRows(t, f, target.DeploymentID, limits)
	// Late environment changes must not alter the captured resource policy.
	t.Setenv("DURPDEPLOY_LIMITS_LOGS_LINE_BYTES", "128")
	var effective config.Limits
	decodeStepLogTest(
		t,
		f.api(t, "GET", "/api/v1/admin/resource-config", nil, 200),
		&effective,
	)
	require.Equal(t, limits, effective.Logs)
	var page repository.LogPage
	body := f.api(t, "GET", target.APIPath+"/page", nil, 200)
	decodeStepLogTest(t, body, &page)
	require.Equal(t, limits.PageRows, page.Limit)
	require.Equal(t, int(limits.PageBytes), page.ByteLimit)
	require.Equal(t, int(limits.LineBytes), page.LineByteLimit)
	require.LessOrEqual(t, int64(len(body)), limits.PageBytes)
	require.Len(t, page.Items, int(limits.PageRows))
	last := page.Items[len(page.Items)-1]
	require.True(t, last.Truncated)
	require.True(t, utf8.ValidString(last.Line))
	require.LessOrEqual(t, int64(len(last.Line)), limits.LineBytes)
	if limits.LineBytes > repository.LogLineByteLimit {
		require.Greater(t, len(last.Line), repository.LogLineByteLimit)
	}
	seen := make(map[int64]bool)
	for {
		for _, row := range page.Items {
			require.False(t, seen[row.ID])
			seen[row.ID] = true
		}
		if page.NextBefore == 0 {
			break
		}
		body = f.api(
			t,
			"GET",
			fmt.Sprintf("%s/page?before=%d", target.APIPath, page.NextBefore),
			nil,
			200,
		)
		decodeStepLogTest(t, body, &page)
		require.LessOrEqual(t, int64(len(body)), limits.PageBytes)
	}
	require.Len(t, seen, 521)
	for _, preview := range []bool{false, true} {
		stream := f.readStepLogs(
			t,
			fmt.Sprintf(
				"%s/stream?format=structured&preview=%t",
				target.APIPath,
				preview,
			),
			"",
		)
		require.Len(t, stream, 521)
		if preview {
			require.True(t, stream[520].Truncated)
			require.Equal(t, last.Line, stream[520].Line)
		} else {
			require.False(t, stream[520].Truncated)
			require.Equal(t, line, stream[520].Line)
		}
	}
	for _, export := range []string{
		string(f.api(t, "GET", target.APIPath+".txt", nil, 200)),
		f.web(t, "GET", target.WebPath+"/logs.txt", nil, 200),
	} {
		require.Contains(t, export, line)
		require.Equal(t, 520, strings.Count(export, "configured-row-"))
	}
	html := f.web(t, "GET", target.WebPath, nil, 200)
	require.Contains(
		t,
		html,
		fmt.Sprintf(
			"Showing up to %d recent entries and %d bytes of previews.",
			limits.PageRows,
			limits.PageBytes,
		),
	)
	require.Contains(
		t,
		html,
		fmt.Sprintf("Lines over %d bytes are shortened.", limits.LineBytes),
	)
	require.NotContains(t, html, "complete-configured-tail")
}

func TestConfiguredPendingLogsAPIE2E(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		t.Run(fmt.Sprint(accepted), func(t *testing.T) {
			testConfiguredPendingLogs(t, accepted)
		})
	}
}

func testConfiguredPendingLogs(t *testing.T, accepted bool) {
	t.Helper()
	limits := configuredLogLimits(accepted)
	script := "printf 'configured-producer-started\\n'\nsleep 0.1\n"
	if !accepted {
		limits.PendingBytes = 4096
		script += "sleep 60 &\n"
	}
	script += "head -c 8192 /dev/zero | tr '\\000' X\n"
	if accepted {
		script += "printf '\\nconfigured-output-ok\\n'\n"
	} else {
		script += "sleep 60\n"
	}
	f := newConfiguredLogE2E(t, limits)
	f.api(t, "POST", f.base()+"/steps", map[string]any{
		"name": "Configured pending output", "container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
		"script_body": script,
	}, 201)
	release := verificationRelease(t, f, "configured-pending")
	deployment := verificationDeploy(t, f, release)
	status := "failed"
	if accepted {
		status = "succeeded"
	}
	logs := string(
		awaitStagingTerminal(
			t,
			f,
			fmt.Sprintf("/api/v1/deployments/%d", deployment.ID),
			status,
		),
	)
	require.Contains(
		t,
		logs,
		"configured-producer-started",
		"workload did not start",
	)
	if accepted {
		require.Contains(t, logs, "configured-output-ok")
	} else {
		require.Contains(t, logs, "pending log output exceeded safety limit")
		require.NotContains(t, logs, "XXXXXXXXXX")
		require.NotContains(t, logs, "configured-output-ok")
	}
	assertStagingPolicyCleanup(t, f)
}
