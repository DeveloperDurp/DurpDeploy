package artifact

import (
	"context"

	"durpdeploy/internal/config"
	agentartifact "github.com/DeveloperDurp/durpdeploy-agent/artifact"
	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
)

func ValidateZIP(ctx context.Context, filename string) error {
	return agentartifact.ValidateZIP(ctx, filename)
}

func ExtractZIP(ctx context.Context, filename string) (string, error) {
	return agentartifact.ExtractZIP(ctx, filename)
}

// ValidateZIPWithLimits applies the captured artifact policy without changing ZIP safety rules.
func ValidateZIPWithLimits(
	ctx context.Context,
	filename string,
	limits config.ArtifactLimits,
) error {
	return agentartifact.ValidateZIPWithPolicy(
		ctx,
		filename,
		resourcepolicy.Artifact(limits),
	)
}

// ExtractZIPWithLimits uses the same policy as the download validator.
func ExtractZIPWithLimits(
	ctx context.Context,
	filename string,
	limits config.ArtifactLimits,
) (string, error) {
	return agentartifact.ExtractZIPWithPolicy(
		ctx,
		filename,
		resourcepolicy.Artifact(limits),
	)
}
