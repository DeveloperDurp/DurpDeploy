package main

import (
	"path/filepath"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/secret"
)

func TestArtifactCredentialsRotateAtomically(t *testing.T) {
	// Given
	conn, err := migrate.Run(filepath.Join(t.TempDir(), "rotation.db"))
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
	repo.SetSecretBox(oldBox)
	project, err := repo.Queries.CreateProject(
		t.Context(),
		db.CreateProjectParams{Name: "rotation"},
	)
	if err != nil {
		t.Fatal(err)
	}
	source, err := repo.CreatePackageRepository(
		t.Context(),
		db.CreatePackageRepositoryParams{
			ProjectID:   project.ID,
			Name:        "packages",
			UrlTemplate: "https://repo.example/{version}.zip",
			AuthType:    "bearer",
			Credential:  "rotate-package-secret",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	key[0] = 1
	newBox, err := secret.NewBox(key)
	if err != nil {
		t.Fatal(err)
	}
	// When
	err = repo.WithTx(t.Context(), func(q *db.Queries) error {
		return rotateArtifactCredentials(
			t.Context(),
			q,
			artifactKeyRotation{oldBox: oldBox, newBox: newBox},
		)
	})
	// Then
	if err != nil {
		t.Fatal(err)
	}
	stored, err := repo.Queries.GetPackageRepository(t.Context(), source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := oldBox.Decrypt(stored.Credential); err == nil {
		t.Fatal("old key still decrypts credential")
	}
	plain, err := newBox.Decrypt(stored.Credential)
	if err != nil || plain != "rotate-package-secret" {
		t.Fatalf("new key cannot recover credential: %v", err)
	}
}

func TestArtifactCredentialsRejectPlaintextRotation(t *testing.T) {
	// Given: repository credentials can only be stored encrypted.
	dsn := filepath.Join(t.TempDir(), "plaintext-rotation.db")
	conn, err := migrate.Run(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	})
	repo := repository.New(conn)
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	repo.SetSecretBox(box)
	project, err := repo.Queries.CreateProject(
		t.Context(),
		db.CreateProjectParams{Name: "plaintext-rotation"},
	)
	if err != nil {
		t.Fatal(err)
	}
	source, err := repo.CreatePackageRepository(
		t.Context(),
		db.CreatePackageRepositoryParams{
			ProjectID:   project.ID,
			Name:        "packages",
			UrlTemplate: "https://repo.example/{version}.zip",
			AuthType:    "bearer",
			Credential:  "preserve-package-secret",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DURPDEPLOY_DB", dsn)
	// When
	code := runSecretKey([]string{"rotate", "--plaintext"})
	// Then: reject the migration and keep the original encrypted value.
	if code == 0 {
		t.Fatal("plaintext rotation accepted encrypted repository credentials")
	}
	stored, err := repo.Queries.GetPackageRepository(t.Context(), source.ID)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := box.Decrypt(stored.Credential)
	if err != nil || plain != "preserve-package-secret" {
		t.Fatalf("credential was corrupted: %v", err)
	}
}
