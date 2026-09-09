package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"durpdeploy/internal/db"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

type RemotePayloadSnapshot struct {
	Agent       db.Agent
	Deployment  db.Deployment
	Release     db.Release
	Environment db.Environment
	Steps       []db.DeploymentStep
	Variables   []db.ReleaseVariable
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

func (r *Repository) ClaimRemoteDeploymentPayload(
	ctx context.Context,
	agentID string,
	prepare func(RemotePayloadSnapshot) (RemotePreparedClaim, error),
) (RemoteClaim, bool, error) {
	var result RemoteClaim
	claimed := false
	err := r.WithTx(ctx, func(q *db.Queries) error {
		locked, err := q.LockClaimAgent(ctx, agentID)
		if err != nil {
			return fmt.Errorf("lock active agent: %w", err)
		}
		if locked == 0 {
			return nil
		}
		now, err := q.CurrentUnixTime(ctx)
		if err != nil {
			return fmt.Errorf("read database time: %w", err)
		}
		waiting, err := q.ListWaitingRemoteDeploymentClaims(ctx, agentID)
		if err != nil {
			return fmt.Errorf("list waiting remote claims: %w", err)
		}
		if len(waiting) == 0 {
			return nil
		}
		candidate := waiting[0]
		locked, err = q.LockWaitingRemoteDeploymentClaim(
			ctx,
			db.LockWaitingRemoteDeploymentClaimParams{
				DeploymentID: candidate.DeploymentID,
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
				DeploymentID: candidate.DeploymentID,
				AgentID:      sql.NullString{String: agentID, Valid: true},
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
			candidate.DeploymentID,
		)
		if err != nil {
			return err
		}
		prepared, err := prepare(snapshot)
		if err != nil {
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
				DeploymentID: candidate.DeploymentID,
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
			DeploymentID: candidate.DeploymentID,
			Token:        prepared.Token,
			Ciphertext:   prepared.Ciphertext,
		}
		return nil
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
		Agent:       agent,
		Deployment:  deployment,
		Release:     release,
		Environment: environment,
		Steps:       steps,
		Variables:   variables,
	}, nil
}
