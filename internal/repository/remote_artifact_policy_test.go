package repository_test

import (
	"fmt"
	"testing"

	"durpdeploy/internal/config"
)

func TestCustomArtifactPolicyRequiresMatchingAgent(t *testing.T) {
	for _, field := range []string{"download", "download higher", "bytes", "files", "nodes", "metadata", "fetch timeout"} {
		for _, withPackage := range []bool{false, true} {
			t.Run(
				fmt.Sprintf("%s/package=%t", field, withPackage),
				func(t *testing.T) {
					settings := config.Defaults()
					switch field {
					case "download":
						settings.Limits.Artifact.DownloadBytes = 1024
					case "download higher":
						settings.Limits.Artifact.DownloadBytes++
					case "bytes":
						settings.Limits.Artifact.ExtractedBytes = 1024
					case "files":
						settings.Limits.Artifact.Files = 2
					case "nodes":
						settings.Limits.Artifact.Files = 2
						settings.Limits.Artifact.StagingNodes = 4
					case "metadata":
						settings.Limits.Artifact.MetadataBytes = 8192
					case "fetch timeout":
						settings.Limits.Artifact.FetchTimeoutSeconds++
					}
					testCustomResourcePolicyBlocksRemoteQueueAndClaims(
						t,
						settings,
						withPackage,
					)
				},
			)
		}
	}
}
