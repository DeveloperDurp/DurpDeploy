package runner

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/secret"
)

func TestShutdownWaitsForApprovedStagingRestore(t *testing.T) {
	r, repo, trace := podmanFixture(t, "")
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	repo.SetSecretBox(box)
	dep := stagingDeployment(t, repo)
	var bundle bytes.Buffer
	writer := tar.NewWriter(&bundle)
	data := "approved bundle"
	if err := writer.WriteHeader(&tar.Header{
		Name: "plan", Mode: 0644, Size: int64(len(data)),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte(data)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(data))
	checksum := hex.EncodeToString(hash[:])
	bundleHash := sha256.Sum256(bundle.Bytes())
	bundleChecksum := hex.EncodeToString(bundleHash[:])
	ciphertext, err := box.Encrypt(
		fmt.Sprintf("%d/0/0\n%s", dep.ID, bundle.Bytes()),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB.ExecContext(t.Context(),
		`INSERT INTO artifact_gate_runs(deployment_id,next_step)
VALUES(?,1)`, dep.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB.ExecContext(t.Context(), `
INSERT INTO artifact_gates(deployment_id,step_index,status,artifact_path,
artifact_sha256,bundle_sha256,bundle_size,review,created_at,expires_at,approved_by)
VALUES(?,0,'approved','plan',?,?,?,'{}',0,?,1)`,
		dep.ID, checksum, bundleChecksum, bundle.Len(), time.Now().Unix()+60); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB.ExecContext(t.Context(), `
INSERT INTO artifact_gate_chunks(deployment_id,step_index,chunk_index,ciphertext)
VALUES(?,0,0,?)`, dep.ID, ciphertext); err != nil {
		t.Fatal(err)
	}
	image := "sha256:" + strings.Repeat("a", 64)
	for index := range int64(2) {
		if err := repo.Queries.CreateArtifactGateImage(t.Context(),
			db.CreateArtifactGateImageParams{
				DeploymentID: dep.ID, StepIndex: index, ImageID: image,
			}); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("STAGING_RESTORE_IMAGE", image)
	cli := `#!/bin/sh
case "$3:$4" in
inspect:*) printf '%s\n' "$STAGING_RESTORE_IMAGE";;
volume:create)
  touch "$PODMAN_TRACE.started"
  exec sh -c 'while [ ! -f "$PODMAN_TRACE.release" ]; do sleep 0.01; done';;
rm:*|volume:rm) touch "$PODMAN_TRACE.removed";;
esac
`
	if err := os.WriteFile(r.engine.binary, []byte(cli), 0o700); err != nil {
		t.Fatal(err)
	}
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		r.Run(t.Context(), dep.ID, dep.ReleaseID, dep.EnvironmentID)
	}()
	var shutdownDone chan struct{}
	t.Cleanup(func() {
		if err := os.WriteFile(trace+".release", nil, 0o600); err != nil {
			t.Error(err)
		}
		for _, done := range []chan struct{}{runDone, shutdownDone} {
			if done == nil {
				continue
			}
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Error("restoration shutdown did not finish")
			}
		}
	})
	deadline := time.Now().Add(3 * time.Second)
	for {
		select {
		case <-runDone:
			stored, _ := repo.Queries.GetDeployment(t.Context(), dep.ID)
			logs, _ := repo.Queries.ListDeploymentLogsByDeployment(
				t.Context(),
				dep.ID,
			)
			t.Fatalf(
				"restoration stopped: deployment=%+v logs=%+v",
				stored,
				logs,
			)
		default:
		}
		if _, err := os.Stat(trace + ".started"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("approved staging restoration did not start")
		}
		time.Sleep(time.Millisecond)
	}
	shutdownDone = make(chan struct{})
	go func() {
		defer close(shutdownDone)
		r.KillAll()
	}()
	select {
	case <-shutdownDone:
		t.Fatal(
			"shutdown completed while approved staging creation was pending",
		)
	case <-time.After(100 * time.Millisecond):
	}
	if _, err := os.Stat(trace + ".removed"); !os.IsNotExist(err) {
		t.Fatalf("shutdown removed staging during creation: %v", err)
	}
	if err := os.WriteFile(trace+".release", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-shutdownDone:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not drain restored staging")
	}
	stored, err := repo.Queries.GetDeployment(t.Context(), dep.ID)
	if err != nil || stored.Status != "cancelled" || len(r.staging) != 0 {
		t.Fatalf("deployment=%+v staging=%v error=%v", stored, r.staging, err)
	}
}
