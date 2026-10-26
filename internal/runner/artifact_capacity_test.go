package runner

import (
	"os"
	"testing"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/config"
)

func TestArtifactCapacityIncludesPerFilePageRounding(t *testing.T) {
	// Given: the review's near-limit archive fits logical size, not rounded size.
	const fileSize = int64(53249)
	page := int64(os.Getpagesize())
	allocated := ((fileSize + page - 1) / page) * page * int64(
		artifact.MaxFiles,
	)
	// When
	capacity := artifactStagingCapacity(config.Defaults().Limits.Artifact)
	// Then
	if capacity < allocated ||
		capacity < artifact.MaxExtracted+int64(artifact.MaxFiles)*page {
		t.Fatalf(
			"capacity %d does not cover page-rounded files %d",
			capacity,
			allocated,
		)
	}
}

func TestConfiguredArtifactCapacityIncludesEveryNode(t *testing.T) {
	for _, nodes := range []int64{4, 40000} {
		limits := config.Defaults().Limits.Artifact
		limits.ExtractedBytes = 2048
		limits.Files = min(limits.Files, nodes)
		limits.StagingNodes = nodes
		want := int64(2048) + nodes*int64(os.Getpagesize())
		if got := artifactStagingCapacity(limits); got != want {
			t.Fatalf("capacity=%d want=%d", got, want)
		}
	}
}
