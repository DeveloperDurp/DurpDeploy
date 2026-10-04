package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/secret"
)

func seedRotationGate(
	t *testing.T,
	repo *repository.Repository,
) (db.ArtifactGate, []byte) {
	t.Helper()
	project, err := repo.Queries.CreateProject(t.Context(),
		db.CreateProjectParams{Name: "gate-rotation"})
	if err != nil {
		t.Fatal(err)
	}
	env, err := repo.Queries.CreateEnvironment(t.Context(),
		db.CreateEnvironmentParams{Name: "gate-rotation"})
	if err != nil {
		t.Fatal(err)
	}
	release, err := repo.Queries.CreateRelease(
		t.Context(),
		db.CreateReleaseParams{
			ProjectID: project.ID,
			Version:   "1",
			StepsJson: "[]",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := repo.Queries.CreateDeployment(t.Context(),
		db.CreateDeploymentParams{
			ReleaseID: release.ID, EnvironmentID: env.ID, Status: "running",
		})
	if err != nil {
		t.Fatal(err)
	}
	data := bytes.Repeat([]byte("sensitive-plan"), 100000)
	hash := sha256.Sum256(data)
	checksum := hex.EncodeToString(hash[:])
	if err := repo.SaveArtifactGate(t.Context(), db.CreateArtifactGateParams{
		DeploymentID: deployment.ID, StepIndex: 0, ArtifactPath: "plan",
		ArtifactSha256: checksum, BundleSha256: checksum,
		BundleSize: int64(len(data)), Review: `{}`,
	}, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	gate, err := repo.Queries.GetArtifactGate(t.Context(),
		db.GetArtifactGateParams{DeploymentID: deployment.ID})
	if err != nil {
		t.Fatal(err)
	}
	return gate, data
}

func TestArtifactGateKeyRotationFailureIsAtomic(t *testing.T) {
	for _, failure := range []string{"plaintext", "corrupt"} {
		t.Run(failure, func(t *testing.T) {
			conn, err := migrate.Run(filepath.Join(t.TempDir(), "gate.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			repo := repository.New(conn)
			oldBox, err := secret.NewBox(make([]byte, 32))
			if err != nil {
				t.Fatal(err)
			}
			repo.SetSecretBox(oldBox)
			gate, _ := seedRotationGate(t, repo)
			key := db.GetArtifactGateChunkParams{
				DeploymentID: gate.DeploymentID,
			}
			original, err := repo.Queries.GetArtifactGateChunk(t.Context(), key)
			if err != nil {
				t.Fatal(err)
			}
			if failure == "corrupt" {
				if err := repo.Queries.UpdateArtifactGateChunkCiphertext(
					t.Context(),
					db.UpdateArtifactGateChunkCiphertextParams{
						DeploymentID: gate.DeploymentID, ChunkIndex: 1,
						Ciphertext: "corrupt",
					},
				); err != nil {
					t.Fatal(err)
				}
			}
			newKey := make([]byte, 32)
			newKey[0] = 1
			newBox, err := secret.NewBox(newKey)
			if err != nil {
				t.Fatal(err)
			}
			err = repo.WithTx(t.Context(), func(q *db.Queries) error {
				return rotateStoredCredentials(t.Context(), q,
					secretKeyRotation{oldBox: oldBox, newBox: newBox,
						plaintext: failure == "plaintext"})
			})
			if err == nil {
				t.Fatal("unsafe rotation succeeded")
			}
			current, err := repo.Queries.GetArtifactGateChunk(t.Context(), key)
			if err != nil || current != original {
				t.Fatal("failed rotation changed an earlier encrypted chunk")
			}
		})
	}
}
