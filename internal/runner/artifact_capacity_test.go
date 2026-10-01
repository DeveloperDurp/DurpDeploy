package runner

import (
	"os"
	"testing"

	"durpdeploy/internal/artifact"
)

func TestArtifactCapacityIncludesPerFilePageRounding(t *testing.T) {
	// Given: the review's near-limit archive fits logical size, not rounded size.
	const fileSize = int64(53249)
	page := int64(os.Getpagesize())
	allocated := ((fileSize + page - 1) / page) * page * int64(
		artifact.MaxFiles,
	)
	// When
	capacity := artifactStagingCapacity()
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
