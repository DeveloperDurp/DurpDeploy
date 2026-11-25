package config

import "github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"

// AgentPolicy adapts the captured server limits to the shared library type.
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
