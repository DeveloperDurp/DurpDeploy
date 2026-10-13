package repository_test

import (
	"archive/tar"
	"context"
	"durpdeploy/internal/testutil"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/secret"
	"durpdeploy/internal/staging"
)

func TestStagingStorageLifecycle(t *testing.T) {
	runStagingStorageParity(t, remoteFixture(t))
}

func runStagingStorageParity(t *testing.T, r *repository.Repository) {
	t.Helper()
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	r.SetSecretBox(box)
	environment, err := r.Queries.CreateEnvironment(
		t.Context(),
		db.CreateEnvironmentParams{Name: "staging-storage"},
	)
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := r.Queries.CreateDeployment(
		t.Context(),
		db.CreateDeploymentParams{
			ReleaseID:     1,
			EnvironmentID: environment.ID,
			Status:        "running",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.CreateTemp(t.TempDir(), "input")
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, file)
	writer := tar.NewWriter(file)
	content := strings.Repeat("encrypted-staging-content", 20000)
	if err := writer.WriteHeader(&tar.Header{Name: "nested/output", Size: int64(len(content)), Typeflag: tar.TypeReg, Mode: 0600}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(writer, content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	key := db.GetStagingSnapshotParams{DeploymentID: deployment.ID}
	request := repository.StagingWrite{Key: key, Source: file}
	if err := r.SaveStaging(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if err := r.SaveStaging(t.Context(), request); err != nil {
		t.Fatalf("identical retry: %v", err)
	}
	loaded, err := r.LoadStaging(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := staging.Read(t.Context(), loaded)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Entries["nested/output"].Size != int64(len(content)) {
		t.Fatal("content truncated")
	}
	if err := loaded.Close(); err != nil {
		t.Fatal(err)
	}
	chunkKey := db.GetStagingChunkParams{DeploymentID: deployment.ID}
	ciphertext, err := r.Queries.GetStagingChunk(t.Context(), chunkKey)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ciphertext, "encrypted-staging-content") {
		t.Fatal("plaintext content stored")
	}
	if _, err := r.DB.ExecContext(t.Context(), "UPDATE deployment_staging_chunks SET ciphertext=? WHERE deployment_id=? AND chunk_index=0", ciphertext[:len(ciphertext)-1]+"!", deployment.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.LoadStaging(t.Context(), key); !errors.Is(
		err,
		staging.ErrInvalid,
	) {
		t.Fatalf("tampered chunk: %v", err)
	}
	if err := r.WithDeploymentTx(t.Context(), deployment.ID, func(ctx context.Context, q *db.Queries) error {
		return q.UpdateDeploymentStatus(ctx, db.UpdateDeploymentStatusParams{ID: deployment.ID, Status: "failed"})
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := r.Queries.ListStagingSnapshots(t.Context(), deployment.ID)
	if err != nil || len(rows) != 0 {
		t.Fatalf("terminal rows=%v err=%v", rows, err)
	}
	if _, err := r.Queries.GetStagingChunk(t.Context(), chunkKey); err == nil {
		t.Fatal("terminal chunks retained")
	}
	if err := r.SaveStaging(t.Context(), request); !errors.Is(
		err,
		repository.ErrRemoteLifecycleConflict,
	) {
		t.Fatalf("late save: %v", err)
	}
}
