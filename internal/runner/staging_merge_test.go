package runner

import (
	"archive/tar"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/secret"
	"durpdeploy/internal/staging"
	"durpdeploy/internal/testutil"
)

func stagingMergeFixture(
	t *testing.T,
	count int,
) (*DeploymentRunner, *repository.Repository, db.GetStagingSnapshotParams) {
	t.Helper()
	r, repo, _ := podmanFixture(t, "")
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	repo.SetSecretBox(box)
	dep := stagingDeployment(t, repo)
	if err := repo.Queries.UpdateDeploymentStatus(t.Context(), db.UpdateDeploymentStatusParams{ID: dep.ID, Status: "running"}); err != nil {
		t.Fatal(err)
	}
	for i := range count {
		id := fmt.Sprintf("merge-%03d", i)
		if _, err := repo.DB.ExecContext(t.Context(), "INSERT INTO agents (id,name,endpoint) VALUES (?,?,'https://agent')", id, id); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.DB.ExecContext(t.Context(), "INSERT INTO remote_step_runs (deployment_id,step_index,agent_id,state) VALUES (?,0,?,'succeeded')", dep.ID, id); err != nil {
			t.Fatal(err)
		}
	}
	return r, repo, db.GetStagingSnapshotParams{DeploymentID: dep.ID}
}

func saveMergeSnapshot(
	t *testing.T,
	repo *repository.Repository,
	key db.GetStagingSnapshotParams,
	values map[string]string,
) {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "snapshot")
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, file)
	w := tar.NewWriter(file)
	for name, value := range values {
		if err := w.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(value))}); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(value)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveStaging(t.Context(), repository.StagingWrite{Key: key, Source: file}); err != nil {
		t.Fatal(err)
	}
}

func TestStagingMergePreflight(t *testing.T) {
	for _, scenario := range []string{"exact bytes", "excess bytes", "exact outputs", "excess outputs", "invalid size", "missing", "unfinished"} {
		t.Run(scenario, func(t *testing.T) {
			checkStagingMergePreflight(t, scenario)
		})
	}
}

func checkStagingMergePreflight(t *testing.T, scenario string) {
	t.Helper()
	count := 3
	if strings.Contains(scenario, "outputs") {
		count = staging.MaxMergeOutputs
	}
	if scenario == "excess outputs" {
		count++
	}
	r, repo, key := stagingMergeFixture(t, count)
	seedStagingMergePreflight(t, repo, key, count, scenario)
	if scenario == "unfinished" {
		if _, err := repo.DB.ExecContext(t.Context(), "UPDATE remote_step_runs SET state='failed' WHERE deployment_id=?", key.DeploymentID); err != nil {
			t.Fatal(err)
		}
	}
	_, err := r.stagingMergeInputs(t.Context(), key)
	var want error
	switch scenario {
	case "excess bytes", "excess outputs":
		want = staging.ErrMergeBudget
	case "invalid size":
		want = staging.ErrInvalid
	case "missing":
		want = sql.ErrNoRows
	case "unfinished":
		want = repository.ErrRemoteLifecycleConflict
	}
	if !errors.Is(err, want) {
		t.Fatalf("preflight=%v, want %v", err, want)
	}
	if want != nil {
		// None of these metadata fixtures has encrypted chunks. Rejection
		// must happen before a missing chunk can replace the budget error.
		result, err := r.mergeRemoteStaging(t.Context(), key)
		if result != nil || !errors.Is(err, want) {
			t.Fatalf("merge=%v %v", result, err)
		}
	}
}

func seedStagingMergePreflight(
	t *testing.T,
	repo *repository.Repository,
	key db.GetStagingSnapshotParams,
	count int,
	scenario string,
) {
	t.Helper()
	remaining := staging.MaxMergeArchiveBytes
	for i := -1; i < count; i++ {
		producer := ""
		if i >= 0 {
			producer = fmt.Sprintf("merge-%03d", i)
		}
		size := int64(1024)
		if strings.Contains(scenario, "bytes") {
			size = min(remaining, staging.MaxArchive)
			remaining -= size
			if i == count-1 && scenario == "excess bytes" {
				size++
			}
		}
		if i == 0 && scenario == "invalid size" {
			size = 1
		}
		if i == 0 && scenario == "missing" {
			continue
		}
		if err := repo.Queries.CreateStagingSnapshot(t.Context(), db.CreateStagingSnapshotParams{DeploymentID: key.DeploymentID, Producer: producer, Sha256: strings.Repeat("0", 64), Size: size}); err != nil {
			t.Fatal(err)
		}
	}
}

type stagingMergeProbe struct {
	db.DBTX
	observe func(string)
}

func (p stagingMergeProbe) QueryRowContext(
	ctx context.Context,
	query string,
	args ...any,
) *sql.Row {
	if strings.Contains(query, "name: GetStagingChunk") {
		p.observe(args[2].(string))
	}
	return p.DBTX.QueryRowContext(ctx, query, args...)
}

func stagingSpoolCount(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skip("descriptor observation requires procfs")
	}
	n := 0
	for _, entry := range entries {
		name, _ := os.Readlink(filepath.Join("/proc/self/fd", entry.Name()))
		if strings.Contains(name, "durpdeploy-gate-") {
			n++
		}
	}
	return n
}

func TestStagingMergeClosesProducerSpools(t *testing.T) {
	for _, scenario := range []string{"identical", "disjoint", "conflict", "corrupt", "cancel", "delete failure"} {
		t.Run(scenario, func(t *testing.T) {
			checkStagingMergeSpools(t, scenario)
		})
	}
}

func checkStagingMergeSpools(t *testing.T, scenario string) {
	t.Helper()
	r, repo, key := stagingMergeSpoolFixture(t, scenario)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	before, peak := stagingSpoolCount(t), 0
	repo.Queries = db.New(
		stagingMergeProbe{
			DBTX: repo.DB,
			observe: func(producer string) {
				peak = max(peak, stagingSpoolCount(t)-before)
				if scenario == "cancel" && producer == "merge-001" {
					cancel()
				}
			},
		},
	)
	result, err := r.mergeRemoteStaging(ctx, key)
	if scenario == "identical" || scenario == "disjoint" {
		assertStagingMergeSuccess(t, repo, key, scenario, result, err)
	} else {
		assertStagingMergeFailure(t, ctx, scenario, result, err)
	}
	if peak > 3 {
		t.Fatalf("retained producer spools: peak=%d", peak)
	}
	if after := stagingSpoolCount(t); after != before {
		t.Fatalf("spool leak: before=%d after=%d", before, after)
	}
}

func stagingMergeSpoolFixture(
	t *testing.T,
	scenario string,
) (*DeploymentRunner, *repository.Repository, db.GetStagingSnapshotParams) {
	t.Helper()
	r, repo, key := stagingMergeFixture(t, 8)
	saveMergeSnapshot(t, repo, key, map[string]string{"base": "stable"})
	for i := range 8 {
		key.Producer = fmt.Sprintf("merge-%03d", i)
		values := map[string]string{"base": "stable", "shared": "same"}
		if scenario == "disjoint" {
			values = map[string]string{
				"base":       "stable",
				key.Producer: key.Producer,
			}
		}
		if scenario == "conflict" {
			values["shared"] = key.Producer
		}
		saveMergeSnapshot(t, repo, key, values)
	}
	key.Producer = ""
	if scenario == "corrupt" {
		if _, err := repo.DB.ExecContext(t.Context(), "DELETE FROM deployment_staging_chunks WHERE deployment_id=? AND producer='merge-001'", key.DeploymentID); err != nil {
			t.Fatal(err)
		}
	}
	if scenario == "delete failure" {
		if _, err := repo.DB.ExecContext(t.Context(), "CREATE TRIGGER reject_stage_delete BEFORE DELETE ON deployment_staging_snapshots BEGIN SELECT RAISE(FAIL,'delete failed'); END"); err != nil {
			t.Fatal(err)
		}
	}
	return r, repo, key
}

func assertStagingMergeSuccess(
	t *testing.T,
	repo *repository.Repository,
	key db.GetStagingSnapshotParams,
	scenario string,
	result *os.File,
	err error,
) {
	t.Helper()
	if err != nil || result == nil {
		t.Fatalf("merge: %v %v", result, err)
	}
	merged, err := staging.Read(t.Context(), result)
	if err != nil {
		t.Fatal(err)
	}
	want := 2
	if scenario == "disjoint" {
		want = 9
	}
	if len(merged.Entries) != want {
		t.Fatalf("entries=%d want %d", len(merged.Entries), want)
	}
	testutil.Close(t, result)
	rows, err := repo.Queries.ListStagingSnapshots(
		t.Context(),
		key.DeploymentID,
	)
	if err != nil || len(rows) != 0 {
		t.Fatalf("step staging retained: %v %v", rows, err)
	}
}

func assertStagingMergeFailure(
	t *testing.T,
	ctx context.Context,
	scenario string,
	result *os.File,
	err error,
) {
	t.Helper()
	if err == nil || result != nil {
		t.Fatalf("failed merge returned %v %v", result, err)
	}
	if scenario == "conflict" && !errors.Is(err, staging.ErrConflict) {
		t.Fatal(err)
	}
	if scenario == "cancel" && ctx.Err() == nil {
		t.Fatal("cancellation did not reach the merge context")
	}
}
