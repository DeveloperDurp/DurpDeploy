package artifact

import (
	"context"
	"io"

	agentartifact "github.com/DeveloperDurp/durpdeploy-agent/artifact"
)

// WriteTar transports the validated package tree with fixed metadata.
func WriteTar(
	ctx context.Context,
	directory string,
	target io.Writer,
) error {
	return agentartifact.WriteTar(ctx, directory, target)
}
