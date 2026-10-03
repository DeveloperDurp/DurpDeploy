package main

import (
	"path/filepath"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/secret"
)

type verificationKeyFixture struct {
	repo                        *repository.Repository
	oldBox, newBox              *secret.Box
	environmentID, deploymentID int64
}

const verificationKeyTarget = "https://example.test/health?token=key-proof"

func newVerificationKeyFixture(t *testing.T) verificationKeyFixture {
	t.Helper()
	conn, err := migrate.Run(filepath.Join(t.TempDir(), "verification-key.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	})
	repo := repository.New(conn)
	oldBox, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	key[0] = 1
	newBox, err := secret.NewBox(key)
	if err != nil {
		t.Fatal(err)
	}
	repo.SetSecretBox(oldBox)
	project, err := repo.Queries.CreateProject(
		t.Context(),
		db.CreateProjectParams{Name: "key-test"},
	)
	if err != nil {
		t.Fatal(err)
	}
	env, err := repo.CreateEnvironment(t.Context(), db.CreateEnvironmentParams{
		Name:                       "prod",
		VerificationType:           "http",
		VerificationTarget:         verificationKeyTarget,
		VerificationTimeoutSeconds: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	release, err := repo.Queries.CreateRelease(
		t.Context(),
		db.CreateReleaseParams{
			ProjectID: project.ID, Version: "key-v1", StepsJson: "[]",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := repo.CreateDeployment(
		t.Context(),
		db.CreateDeploymentParams{
			ReleaseID: release.ID, EnvironmentID: env.ID, Status: "pending",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return verificationKeyFixture{
		repo,
		oldBox,
		newBox,
		env.ID,
		deployment.Deployment.ID,
	}
}

func TestVerificationTargetsRotateAtomically(t *testing.T) {
	f := newVerificationKeyFixture(t)
	if err := f.repo.WithTx(t.Context(), func(q *db.Queries) error {
		return rotateStoredCredentials(t.Context(), q,
			secretKeyRotation{oldBox: f.oldBox, newBox: f.newBox})
	}); err != nil {
		t.Fatal(err)
	}
	env, err := f.repo.Queries.GetEnvironment(t.Context(), f.environmentID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.oldBox.Decrypt(env.VerificationTarget); err == nil {
		t.Fatal("old key decrypts rotated target")
	}
	f.repo.SetSecretBox(f.newBox)
	assertVerificationKeyTargets(t, f)
}

func TestVerificationTargetsRejectPlaintextRotation(t *testing.T) {
	f := newVerificationKeyFixture(t)
	if err := f.repo.WithTx(t.Context(), func(q *db.Queries) error {
		return rotateStoredCredentials(t.Context(), q,
			secretKeyRotation{newBox: f.newBox, plaintext: true})
	}); err == nil {
		t.Fatal("plaintext rotation accepted encrypted targets")
	}
	assertVerificationKeyTargets(t, f)
}

func assertVerificationKeyTargets(t *testing.T, f verificationKeyFixture) {
	t.Helper()
	env, err := f.repo.GetEnvironment(t.Context(), f.environmentID)
	if err != nil || env.VerificationTarget != verificationKeyTarget {
		t.Fatalf("environment target lost: %v", err)
	}
	check, err := f.repo.Queries.GetDeploymentVerification(
		t.Context(),
		f.deploymentID,
	)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := f.repo.DecryptVerificationTarget(check.Target)
	if err != nil || plain != verificationKeyTarget {
		t.Fatalf("frozen target lost: %v", err)
	}
}

func TestVerificationTargetRotationFailurePreservesEnvironment(t *testing.T) {
	f := newVerificationKeyFixture(t)
	before, err := f.repo.Queries.GetEnvironment(t.Context(), f.environmentID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.repo.Queries.UpdateDeploymentVerificationTarget(t.Context(),
		db.UpdateDeploymentVerificationTargetParams{
			DeploymentID: f.deploymentID, Target: "corrupt-ciphertext",
		}); err != nil {
		t.Fatal(err)
	}
	if err := f.repo.WithTx(t.Context(), func(q *db.Queries) error {
		return rotateStoredCredentials(t.Context(), q,
			secretKeyRotation{oldBox: f.oldBox, newBox: f.newBox})
	}); err == nil {
		t.Fatal("rotation accepted a corrupt frozen target")
	}
	after, err := f.repo.Queries.GetEnvironment(t.Context(), f.environmentID)
	if err != nil || after.VerificationTarget != before.VerificationTarget {
		t.Fatal("failed rotation committed a partial environment change")
	}
}
