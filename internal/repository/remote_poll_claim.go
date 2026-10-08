package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"durpdeploy/internal/db"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

type RemotePayloadSnapshot struct {
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
}

type RemotePreparedClaim struct {
	Token      string
	TokenHash  []byte
	Ciphertext []byte
}

type RemoteClaim struct {
	DeploymentID int64
	Token        string
	Ciphertext   []byte
}

// ClaimRemoteDeploymentPayload may repeat prepare after a rolled-back claim.
// The callback must only prepare the payload, without external side effects.
func (r *Repository) ClaimRemoteDeploymentPayload(
	ctx context.Context,
	agentID string,
	prepare func(RemotePayloadSnapshot) (RemotePreparedClaim, error),
) (RemoteClaim, bool, error) {
	var result RemoteClaim
	claimed := false
	err := withPollClaimRetry(ctx, func() error {
		result = RemoteClaim{}
		claimed = false
		waiting, err := r.Queries.ListWaitingRemoteDeploymentClaims(
			ctx,
			agentID,
		)
		if err != nil || len(waiting) == 0 {
			return err
		}
		candidate := waiting[0]
		return r.WithDeploymentTx(
			ctx,
			candidate,
			func(ctx context.Context, q *db.Queries) error {
				locked, err := q.LockClaimAgent(ctx, agentID)
				if err != nil {
					return fmt.Errorf("lock active agent: %w", err)
				}
				if locked == 0 {
					return nil
				}
				agent, err := q.GetAgent(ctx, agentID)
				if err != nil || agent.Draining != 0 {
					return err
				}
				now, err := q.CurrentUnixTime(ctx)
				if err != nil {
					return fmt.Errorf("read database time: %w", err)
				}
				waiting, err := q.ListWaitingRemoteDeploymentClaims(
					ctx,
					agentID,
				)
				if err != nil {
					return fmt.Errorf("list waiting remote claims: %w", err)
				}
				if len(waiting) == 0 ||
					waiting[0] != candidate {
					return nil
				}
				locked, err = q.LockWaitingRemoteDeploymentClaim(
					ctx,
					db.LockWaitingRemoteDeploymentClaimParams{
						DeploymentID: candidate,
						AgentID:      agentID,
					},
				)
				if err != nil {
					return fmt.Errorf("lock waiting remote claim: %w", err)
				}
				if locked == 0 {
					return nil
				}
				locked, err = q.LockPendingRemoteDeployment(
					ctx,
					db.LockPendingRemoteDeploymentParams{
						DeploymentID: candidate,
						AgentID: sql.NullString{
							String: agentID,
							Valid:  true,
						},
					},
				)
				if err != nil {
					return fmt.Errorf("lock pending deployment: %w", err)
				}
				if locked == 0 {
					return nil
				}
				snapshot, err := r.remotePayloadSnapshot(
					ctx,
					q,
					agentID,
					candidate,
				)
				if err != nil {
					return err
				}
				prepared, err := prepare(snapshot)
				if err != nil {
					if errors.Is(err, agentproto.ErrUnsupportedProtocol) {
						if err := q.FailWaitingRemotePayload(ctx, db.FailWaitingRemotePayloadParams{
							DeploymentID: candidate, AgentID: agentID,
							Now: sql.NullInt64{Int64: now, Valid: true},
						}); err != nil {
							return err
						}
						return q.FailUnsupportedRemoteDeploymentStatus(ctx,
							db.FailUnsupportedRemoteDeploymentStatusParams{
								Now: sql.NullInt64{
									Int64: now,
									Valid: true,
								}, AgentID: agentID,
							})
					}
					return err
				}
				changed, err := q.ClaimRemoteDeployment(
					ctx,
					db.ClaimRemoteDeploymentParams{
						ClaimTokenHash: prepared.TokenHash,
						Ciphertext: sql.NullString{
							String: string(prepared.Ciphertext),
							Valid:  true,
						},
						ClaimExpiresAt: now + int64(
							agentproto.PreStartClaimTimeout/time.Second,
						),
						Now:          now,
						DeploymentID: candidate,
						AgentID:      agentID,
					},
				)
				if err != nil {
					return fmt.Errorf("claim remote deployment: %w", err)
				}
				if changed == 0 {
					return nil
				}
				claimed = true
				result = RemoteClaim{
					DeploymentID: candidate,
					Token:        prepared.Token,
					Ciphertext:   prepared.Ciphertext,
				}
				return nil
			},
		)
	})
	if err != nil {
		return RemoteClaim{}, false, err
	}
	return result, claimed, nil
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
	variables, err := q.ListReleaseVariablesByRelease(ctx, release.ID)
	if err != nil {
		return RemotePayloadSnapshot{}, fmt.Errorf(
			"list claim variables: %w",
			err,
		)
	}
	for index := range variables {
		variables[index], err = r.decryptReleaseVariable(variables[index])
		if err != nil {
			return RemotePayloadSnapshot{}, err
		}
	}
	return RemotePayloadSnapshot{
		Agent:            agent,
		Deployment:       deployment,
		Release:          release,
		Environment:      environment,
		Steps:            steps,
		Variables:        variables,
		HostInterpreters: host, ExecutionModes: modes,
		ContainerRuntimes: runtimes, ContainerInterpreters: container,
	}, nil
}
