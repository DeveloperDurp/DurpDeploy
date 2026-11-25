package repository

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"durpdeploy/internal/db"
)

func (request AgentPollCapabilities) matchesStoredCapabilities(
	ctx context.Context,
	queries *db.Queries,
	agentID string,
) (bool, error) {
	stored, err := queries.ListAgentPollCapabilities(ctx, agentID)
	if err != nil {
		return false, fmt.Errorf("read agent capabilities: %w", err)
	}
	reported := []db.ListAgentPollCapabilitiesRow{{
		Category: "protocol", Capability: string(request.Protocol),
	}}
	for _, value := range request.SupportedInterpreters {
		reported = append(reported, db.ListAgentPollCapabilitiesRow{
			Category: "host", Capability: string(value),
		})
	}
	for _, value := range request.ExecutionModes {
		reported = append(reported, db.ListAgentPollCapabilitiesRow{
			Category: "mode", Capability: string(value),
		})
	}
	for _, value := range request.ContainerRuntimes {
		reported = append(reported, db.ListAgentPollCapabilitiesRow{
			Category: "runtime", Capability: string(value),
		})
	}
	for _, value := range request.ContainerInterpreters {
		reported = append(reported, db.ListAgentPollCapabilitiesRow{
			Category: "container", Capability: string(value),
		})
	}
	staging := db.ListAgentPollCapabilitiesRow{Category: "staging"}
	// Staging remains durable when an individual request omits support.
	if request.Staging || slices.Contains(stored, staging) {
		reported = append(reported, staging)
	}
	if request.Artifact {
		reported = append(reported, db.ListAgentPollCapabilitiesRow{
			Category: "artifact",
		})
	}
	slices.SortFunc(reported, func(a, b db.ListAgentPollCapabilitiesRow) int {
		if order := cmp.Compare(a.Category, b.Category); order != 0 {
			return order
		}
		return cmp.Compare(a.Capability, b.Capability)
	})
	return slices.Equal(stored, reported), nil
}
