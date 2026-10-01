package main

import (
	"os"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/repository"
)

// Keep the lease until process exit: HTTP shutdown can time out while artifact
// requests still run. Releasing it earlier permits a second server to sweep
// files still used by those goroutines.
var serverArtifactWorkspace *artifact.Workspace

func prepareArtifactWorkspace(repo *repository.Repository) error {
	workspace, err := artifact.OpenWorkspace(os.TempDir())
	if err != nil {
		return err
	}
	serverArtifactWorkspace = workspace
	repo.ArtifactClient.TempDir = workspace.Directory
	return nil
}
