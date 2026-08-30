// Package dispatch routes deployments to the local runner or an assigned remote agent.
package dispatch

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/secret"
)

type Dispatcher struct {
	repo   *repository.Repository
	box    *secret.Box
	runner *runner.DeploymentRunner
}

type payload struct {
	DeploymentID int64               `json:"deployment_id"`
	Release      releaseSnapshot     `json:"release"`
	Environment  environmentSnapshot `json:"environment"`
	Variables    []variableSnapshot  `json:"variables"`
}

type releaseSnapshot struct {
	ID        int64           `json:"id"`
	ProjectID int64           `json:"project_id"`
	Version   string          `json:"version"`
	Steps     json.RawMessage `json:"steps"`
}

type environmentSnapshot struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type variableSnapshot struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Secret bool   `json:"secret"`
}

func New(
	repo *repository.Repository,
	box *secret.Box,
	deploymentRunner *runner.DeploymentRunner,
) *Dispatcher {
	return &Dispatcher{repo: repo, box: box, runner: deploymentRunner}
}

// Dispatch records an immutable routing decision. Only an explicit environment
// assignment selects the remote path; every other environment runs locally.
func (d *Dispatcher) Dispatch(ctx context.Context, deploymentID int64) error {
	var localDeployment db.Deployment
	err := d.repo.WithTx(ctx, func(q *db.Queries) error {
		if _, err := q.GetDeploymentDispatch(ctx, deploymentID); err == nil {
			return nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("get deployment dispatch: %w", err)
		}

		deployment, err := q.GetDeployment(ctx, deploymentID)
		if err != nil {
			return fmt.Errorf("get deployment: %w", err)
		}
		if deployment.Status == "pending_approval" {
			return nil
		}
		assignment, err := q.GetEnvironmentAgentAssignment(
			ctx,
			deployment.EnvironmentID,
		)
		if err == nil {
			if d.box == nil {
				return errors.New("remote dispatch requires a secret box")
			}
			ciphertext, payloadErr := d.buildPayload(ctx, q, deployment)
			if payloadErr != nil {
				return payloadErr
			}
			if _, payloadErr = q.CreateDeploymentPayload(
				ctx,
				db.CreateDeploymentPayloadParams{
					DeploymentID: deployment.ID,
					Ciphertext:   ciphertext,
				},
			); payloadErr != nil {
				return fmt.Errorf("create deployment payload: %w", payloadErr)
			}
			if _, payloadErr = q.CreateDirectDeploymentDispatch(
				ctx,
				db.CreateDirectDeploymentDispatchParams{
					DeploymentID: deployment.ID,
					AssignedAgentID: sql.NullString{
						String: assignment.AgentID,
						Valid:  true,
					},
				},
			); payloadErr != nil {
				return fmt.Errorf(
					"create direct remote dispatch: %w",
					payloadErr,
				)
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("get environment agent assignment: %w", err)
		}

		if _, err := q.CreateDeploymentDispatch(
			ctx,
			db.CreateDeploymentDispatchParams{
				DeploymentID: deployment.ID,
				Mode:         "local",
				State:        "waiting",
			},
		); err != nil {
			return fmt.Errorf("create local dispatch: %w", err)
		}
		localDeployment = deployment
		return nil
	})
	if err != nil {
		return fmt.Errorf("dispatch deployment %d: %w", deploymentID, err)
	}
	if localDeployment.ID != 0 && d.runner != nil {
		go d.runner.Run(
			context.Background(),
			localDeployment.ID,
			localDeployment.ReleaseID,
			localDeployment.EnvironmentID,
		)
	}
	return nil
}

func (d *Dispatcher) buildPayload(
	ctx context.Context,
	q *db.Queries,
	deployment db.Deployment,
) (string, error) {
	release, err := q.GetRelease(ctx, deployment.ReleaseID)
	if err != nil {
		return "", fmt.Errorf("get release: %w", err)
	}
	environment, err := q.GetEnvironment(ctx, deployment.EnvironmentID)
	if err != nil {
		return "", fmt.Errorf("get environment: %w", err)
	}
	variables, err := q.ListReleaseVariablesByRelease(ctx, release.ID)
	if err != nil {
		return "", fmt.Errorf("list release variables: %w", err)
	}
	variables = runner.ResolveReleaseVariables(variables, deployment.EnvironmentID)
	payloadVariables := make([]variableSnapshot, 0, len(variables))
	for _, variable := range variables {
		value, err := d.box.Decrypt(variable.Value.String)
		if err != nil {
			return "", fmt.Errorf(
				"decrypt release variable %d: %w",
				variable.ID,
				err,
			)
		}
		payloadVariables = append(payloadVariables, variableSnapshot{
			Name: variable.Name, Value: value, Secret: variable.Secret != 0,
		})
	}
	encoded, err := json.Marshal(payload{
		DeploymentID: deployment.ID,
		Release: releaseSnapshot{
			ID: release.ID, ProjectID: release.ProjectID, Version: release.Version,
			Steps: json.RawMessage(release.StepsJson),
		},
		Environment: environmentSnapshot{
			ID:   environment.ID,
			Name: environment.Name,
		},
		Variables: payloadVariables,
	})
	if err != nil {
		return "", fmt.Errorf("marshal deployment payload: %w", err)
	}
	ciphertext, err := d.box.Encrypt(string(encoded))
	if err != nil {
		return "", fmt.Errorf("encrypt deployment payload: %w", err)
	}
	return ciphertext, nil
}
