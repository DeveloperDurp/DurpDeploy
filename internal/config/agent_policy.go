package config

import "github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"

// AgentPolicy selects the shared fields from the captured server snapshot.
// Server-only settings do not affect agent compatibility.
func (l Limits) AgentPolicy() resourcepolicy.Limits {
	return resourcepolicy.Limits{
		Execution: resourcepolicy.Execution(l.Execution),
		Artifact:  resourcepolicy.Artifact(l.Artifact),
		Staging:   resourcepolicy.Staging(l.Staging),
		Transfer: resourcepolicy.Transfer{
			IdleTimeoutSeconds: l.Transfer.IdleTimeoutSeconds,
		},
		Container: resourcepolicy.Container(l.Container),
	}
}
