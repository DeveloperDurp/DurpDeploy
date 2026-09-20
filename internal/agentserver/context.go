package agentserver

import (
	"context"
	"net/http"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

type agentIDContextKey struct{}

func withAgentID(
	request *http.Request,
	agentID agentproto.AgentID,
) *http.Request {
	ctx := context.WithValue(request.Context(), agentIDContextKey{}, agentID)
	return request.WithContext(ctx)
}

// AgentIDFromContext returns the ID authenticated by the agent mTLS boundary.
func AgentIDFromContext(ctx context.Context) (agentproto.AgentID, bool) {
	agentID, ok := ctx.Value(agentIDContextKey{}).(agentproto.AgentID)
	return agentID, ok
}
