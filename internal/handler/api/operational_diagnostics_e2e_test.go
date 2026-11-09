//go:build e2e

package api_test

import (
	"database/sql"
	"fmt"
	"os"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"github.com/stretchr/testify/require"
)

func TestDeploymentLogOrderingContractAPIE2E(t *testing.T) {
	f := newArtifactE2E(t)
	release, err := f.h.repo.Queries.CreateRelease(
		t.Context(),
		db.CreateReleaseParams{
			ProjectID: f.project.ID, Version: "log-contract", StepsJson: "[]",
		},
	)
	require.NoError(t, err)
	d := seedDeployment(t, f.h.repo, release.ID, f.environment.ID, "failed")
	var ids []int64
	for i := range 510 {
		row, err := f.h.repo.Queries.CreateRemoteDeploymentLog(t.Context(),
			db.CreateRemoteDeploymentLogParams{DeploymentID: d.ID,
				Line: fmt.Sprintf("contract-%d", i), CreatedAt: 100})
		require.NoError(t, err)
		ids = append(ids, row.ID)
	}
	// Legacy producer order differs from insertion order.
	for sequence, index := range []int{1, 0} {
		require.NoError(
			t,
			f.h.repo.Queries.CreateDeploymentLogScope(t.Context(),
				db.CreateDeploymentLogScopeParams{DeploymentID: d.ID,
					LogID: ids[index], Sequence: int64(sequence)}),
		)
	}
	path := fmt.Sprintf("/api/v1/deployments/%d/logs", d.ID)
	var want []int64
	for i := len(ids) - 1; i >= 2; i-- {
		want = append(want, ids[i])
	}
	want = append(want, ids[1], ids[0])
	for _, query := range []string{"", "?limit=100", "?limit=1", ""} {
		var rows []db.DeploymentLog
		decodeStepLogTest(t, f.api(t, "GET", path+query, nil, 200), &rows)
		var got []int64
		for _, row := range rows {
			got = append(got, row.ID)
		}
		require.Equal(t, want, got)
	}
	var page repository.LogPage
	decodeStepLogTest(
		t,
		f.api(t, "GET", path+"/page?limit=100", nil, 200),
		&page,
	)
	require.Len(t, page.Items, 500)
	require.Equal(t, ids[10], page.NextBefore)
	for i, row := range page.Items {
		require.Equal(t, ids[i+10], row.ID)
	}
	var older repository.LogPage
	decodeStepLogTest(t, f.api(
		t,
		"GET",
		fmt.Sprintf(
			"%s/page?before=%d",
			path,
			page.NextBefore,
		),
		nil,
		200,
	), &older)
	require.Len(t, older.Items, 10)
	require.Zero(t, older.NextBefore)
	for i, row := range older.Items {
		require.Equal(t, ids[i], row.ID)
	}
}

func recoveredDeploymentFixture(t *testing.T) (*artifactE2E, db.Deployment) {
	t.Helper()
	f := newArtifactE2E(t)
	release, err := f.h.repo.Queries.CreateRelease(
		t.Context(),
		db.CreateReleaseParams{
			ProjectID: f.project.ID,
			Version:   "recovery-contract",
			StepsJson: "[]",
		},
	)
	require.NoError(t, err)
	d := seedDeployment(t, f.h.repo, release.ID, f.environment.ID, "running")
	_, err = f.h.repo.DB.ExecContext(t.Context(),
		"UPDATE deployments SET note=?, container_namespace=? WHERE id=?",
		"Operator note", os.Getenv("DURPDEPLOY_CONTAINER_RUNTIME")+":"+
			os.Getenv("DURPDEPLOY_CONTAINER_NAMESPACE"), d.ID)
	require.NoError(t, err)
	// Constructing the runner executes the real startup runtime sweep.
	r := runner.New(f.h.repo, f.h.broker)
	require.True(t, r.ContainerRuntimeReady())
	return f, d
}

func TestStartupRecoveryDiagnosticsAPIE2E(t *testing.T) {
	f, d := recoveredDeploymentFixture(t)
	path := fmt.Sprintf("/api/v1/deployments/%d", d.ID)
	var stored db.Deployment
	decodeStepLogTest(t, f.api(t, "GET", path, nil, 200), &stored)
	require.Equal(t, "failed", stored.Status)
	require.Equal(t, sql.NullString{String: "Operator note\n" +
		repository.RecoveryCleanupConfirmed, Valid: true}, stored.Note)
	var page repository.LogPage
	decodeStepLogTest(t, f.api(t, "GET", path+"/logs/page", nil, 200), &page)
	require.Len(t, page.Items, 1)
	require.Equal(t, repository.RecoveryCleanupConfirmed, page.Items[0].Line)
}
