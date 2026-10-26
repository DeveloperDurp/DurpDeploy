package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"durpdeploy/internal/agentexecution"
	"durpdeploy/internal/db"
	agentartifact "github.com/DeveloperDurp/durpdeploy-agent/artifact"
)

type RemotePayloadSnapshot struct {
	Artifact              *agentartifact.Pin
	Staging               bool
	Agent                 db.Agent
	Deployment            db.Deployment
	Release               db.Release
	Environment           db.Environment
	Steps                 []db.DeploymentStep
	Variables             []db.ReleaseVariable
	HostInterpreters      []string
	ExecutionModes        []string
	ContainerRuntimes     []string
	ContainerInterpreters []string
	ResourcePolicy        string
}

func (r *Repository) remotePayloadSnapshot(
	ctx context.Context,
	q *db.Queries,
	agentID string,
	deploymentID int64,
) (RemotePayloadSnapshot, error) {
	agent, err := q.GetAgent(ctx, agentID)
	if err != nil {
		return RemotePayloadSnapshot{}, fmt.Errorf("get claim agent: %w", err)
	}
	policy, err := r.remoteResourcePolicy(ctx, q, agentID)
	if err != nil {
		return RemotePayloadSnapshot{}, err
	}
	host, err := q.ListAgentInterpreters(ctx, agentID)
	if err != nil {
		return RemotePayloadSnapshot{}, err
	}
	modes, err := q.ListAgentExecutionModes(ctx, agentID)
	if err != nil {
		return RemotePayloadSnapshot{}, err
	}
	runtimes, err := q.ListAgentContainerRuntimes(ctx, agentID)
	if err != nil {
		return RemotePayloadSnapshot{}, err
	}
	container, err := q.ListAgentContainerInterpreters(ctx, agentID)
	if err != nil {
		return RemotePayloadSnapshot{}, err
	}
	deployment, err := q.GetDeployment(ctx, deploymentID)
	if err != nil {
		return RemotePayloadSnapshot{}, fmt.Errorf(
			"get claim deployment: %w",
			err,
		)
	}
	release, err := q.GetRelease(ctx, deployment.ReleaseID)
	if err != nil {
		return RemotePayloadSnapshot{}, fmt.Errorf("get claim release: %w", err)
	}
	environment, err := q.GetEnvironment(ctx, deployment.EnvironmentID)
	if err != nil {
		return RemotePayloadSnapshot{}, fmt.Errorf(
			"get claim environment: %w",
			err,
		)
	}
	steps, err := q.ListDeploymentSteps(ctx, deploymentID)
	if err != nil {
		return RemotePayloadSnapshot{}, fmt.Errorf("list claim steps: %w", err)
	}
	for _, step := range steps {
		if err := agentexecution.ValidateLimitsWithPolicy(step.TimeoutSeconds, step.MaxRetries, r.ResourceLimits().Execution); err != nil {
			return RemotePayloadSnapshot{}, fmt.Errorf(
				"step %q: %w",
				step.Name,
				err,
			)
		}
	}
	variables, err := r.deploymentVariables(ctx, q, deploymentID)
	if err != nil {
		return RemotePayloadSnapshot{}, fmt.Errorf(
			"list claim variables: %w",
			err,
		)
	}
	var pin *agentartifact.Pin
	packageRow, err := q.GetDeploymentArtifact(ctx, deploymentID)
	if err == nil {
		pin = &agentartifact.Pin{
			SHA256: packageRow.Sha256,
			Size:   packageRow.Size,
		}
		if err := pin.ValidateWithPolicy(r.ResourceLimits().AgentPolicy().Artifact); err != nil {
			return RemotePayloadSnapshot{}, fmt.Errorf(
				"invalid deployment package pin: %w",
				err,
			)
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return RemotePayloadSnapshot{}, err
	}
	return RemotePayloadSnapshot{
		Artifact:         pin,
		Agent:            agent,
		Deployment:       deployment,
		Release:          release,
		Environment:      environment,
		Steps:            steps,
		Variables:        variables,
		HostInterpreters: host, ExecutionModes: modes,
		ContainerRuntimes: runtimes, ContainerInterpreters: container,
		ResourcePolicy: policy,
	}, nil
}
