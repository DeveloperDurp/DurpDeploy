package pages

import (
	"strings"

	"durpdeploy/internal/db"
)

func agentSoftwareVersion(agent db.Agent) string {
	if !agent.AgentVersion.Valid ||
		strings.TrimSpace(agent.AgentVersion.String) == "" {
		return "Not reported"
	}
	return agent.AgentVersion.String
}
