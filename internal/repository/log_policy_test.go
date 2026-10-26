package repository

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"durpdeploy/internal/config"
	"durpdeploy/internal/db"
	"github.com/stretchr/testify/require"
)

func TestConfiguredLogPolicyAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		original, _ := openDeploymentCreationEngine(
			t,
			newDeploymentCreationEngine(t, name),
		)
		for _, raised := range []bool{false, true} {
			t.Run(fmt.Sprint(raised), func(t *testing.T) {
				testConfiguredLogPolicy(t, original, raised)
			})
		}
	})
}

func testConfiguredLogPolicy(t *testing.T, original *Repository, raised bool) {
	t.Helper()
	settings := config.Defaults()
	settings.Limits.Logs = config.LogLimits{PageBytes: 8192, LineBytes: 256,
		PreviewBatch: 2, PendingBytes: 128, ExportBatch: 3, PageRows: 4}
	if raised {
		settings.Limits.Logs = config.LogLimits{
			PageBytes:    2 << 20,
			LineBytes:    32768,
			PreviewBatch: 65,
			PendingBytes: 1048577,
			ExportBatch:  257,
			PageRows:     501,
		}
	}
	require.NoError(t, settings.Limits.Validate())
	repo := NewConfigured(original.DB, settings)
	limits := settings.Limits.Logs
	deployment, err := repo.Queries.CreateDeployment(
		t.Context(),
		db.CreateDeploymentParams{
			ReleaseID: 1, EnvironmentID: 1, Status: "succeeded",
		},
	)
	require.NoError(t, err)
	// Enough rows cross the configured preview, export, and page batches.
	for index := range 520 {
		_, err := repo.Queries.CreateDeploymentLog(
			t.Context(),
			db.CreateDeploymentLogParams{
				DeploymentID: deployment.ID, Line: fmt.Sprintf("row-%03d", index),
			},
		)
		require.NoError(t, err)
	}
	page, err := repo.ReadLogPage(t.Context(), deployment.ID, 0)
	require.NoError(t, err)
	require.Len(t, page.Items, int(limits.PageRows))
	require.Equal(t, limits.PageRows, page.Limit)
	preview, err := repo.ReadLogPreviewsAfter(t.Context(), deployment.ID, 0)
	require.NoError(t, err)
	require.Len(t, preview, int(limits.PreviewBatch))
	var exported []db.DeploymentLog
	require.NoError(
		t,
		repo.ForEachDeploymentLogByDeploymentAsc(
			t.Context(),
			deployment.ID,
			func(row db.DeploymentLog) error {
				exported = append(exported, row)
				return nil
			},
		),
	)
	require.Len(t, exported, 520)
	for index, row := range exported {
		require.Equal(t, fmt.Sprintf("row-%03d", index), row.Line)
	}
	// Escaped output and step names exercise byte pagination with valid state.
	line := strings.Repeat("\t<界😀", int(limits.LineBytes)/4) + "complete-tail"
	for range 20 {
		_, err := repo.Queries.CreateStepDeploymentLog(
			t.Context(),
			db.CreateStepDeploymentLogParams{
				DeploymentID: deployment.ID, Line: line,
				StepName: nullable(
					strings.Repeat("\t<", 50),
				), StepState: nullable("succeeded"),
			},
		)
		require.NoError(t, err)
	}
	before := int64(0)
	seen := make(map[int64]bool)
	for {
		page, err := repo.ReadLogPage(t.Context(), deployment.ID, before)
		require.NoError(t, err)
		encoded, err := json.Marshal(page)
		require.NoError(t, err)
		require.LessOrEqual(t, int64(len(encoded)+1), limits.PageBytes)
		require.Equal(t, int(limits.PageBytes), page.ByteLimit)
		require.Equal(t, int(limits.LineBytes), page.LineByteLimit)
		require.NotEmpty(t, page.Items)
		for _, row := range page.Items {
			require.False(t, seen[row.ID], "repeated cursor")
			seen[row.ID] = true
			require.True(t, utf8.ValidString(row.Line))
			require.LessOrEqual(t, int64(len(row.Line)), limits.LineBytes)
			if row.Truncated {
				require.NotContains(t, row.Line, "complete-tail")
			}
		}
		if page.NextBefore == 0 {
			break
		}
		require.Equal(t, page.Items[0].ID, page.NextBefore)
		before = page.NextBefore
	}
	require.Len(t, seen, 540)
}
