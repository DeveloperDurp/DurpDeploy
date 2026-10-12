package artifact

import (
	"context"
	agentartifact "github.com/DeveloperDurp/durpdeploy-agent/artifact"
)

func ValidateZIP(ctx context.Context, filename string) error {
	return agentartifact.ValidateZIP(ctx, filename)
}

func ExtractZIP(ctx context.Context, filename string) (string, error) {
	return agentartifact.ExtractZIP(ctx, filename)
}
