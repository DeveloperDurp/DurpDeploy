package main

import (
	"errors"
	"os"
	"testing"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/repository"
)

func TestArtifactWorkspaceStartup(t *testing.T) {
	// Given: a server with a private temporary root.
	base := t.TempDir()
	t.Setenv("TMPDIR", base)
	t.Setenv("TMP", base)
	t.Setenv("TEMP", base)
	repo := repository.New(nil)

	// When: startup configures artifact storage.
	if err := prepareArtifactWorkspace(repo); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := serverArtifactWorkspace.Close(); err != nil {
			t.Error(err)
		}
		serverArtifactWorkspace = nil
	})

	// Then: downloads use leased storage, and another instance cannot sweep it.
	file, err := os.CreateTemp(
		repo.ArtifactClient.TempDir,
		"durpdeploy-artifact-*.zip",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	other, err := artifact.OpenWorkspace(base)
	if other != nil {
		defer other.Close()
	}
	if !errors.Is(err, artifact.ErrWorkspaceBusy) {
		t.Fatalf("startup did not retain lease: %v", err)
	}
}
