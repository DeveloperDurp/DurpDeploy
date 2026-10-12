package dispatch

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

// Dispatcher owns remote dispatch maintenance. Execution is wired separately.
type Dispatcher struct {
	repository *repository.Repository
}

// PollRequest includes capabilities reported outside the JSON envelope.
type PollRequest struct {
	agentproto.PollRequest
	Staging bool
}

func New(repo *repository.Repository) *Dispatcher {
	return &Dispatcher{repository: repo}
}

func (d *Dispatcher) Poll(
	ctx context.Context,
	agentID agentproto.AgentID,
	request PollRequest,
) (agentproto.PollResponse, bool, error) {
	response, claimed, err := d.claim(ctx, agentID, request)
	if errors.Is(err, errStaleAgentPoll) {
		return agentproto.PollResponse{}, false, nil
	}
	if err != nil || claimed {
		return response, claimed, err
	}
	deadline := time.NewTimer(agentproto.PollInterval)
	defer deadline.Stop()
	fallback := time.NewTicker(time.Second)
	defer fallback.Stop()
	for {
		select {
		case <-ctx.Done():
			return agentproto.PollResponse{}, false, ctx.Err()
		case <-d.repository.RemoteWorkReady():
		case <-fallback.C:
		case <-deadline.C:
			response, claimed, err = d.claim(ctx, agentID, request)
			if errors.Is(err, errStaleAgentPoll) {
				return agentproto.PollResponse{}, false, nil
			}
			return response, claimed, err
		}
		response, claimed, err = d.claim(ctx, agentID, request)
		if errors.Is(err, errStaleAgentPoll) {
			return agentproto.PollResponse{}, false, nil
		}
		if err != nil || claimed {
			return response, claimed, err
		}
	}
}

func (d *Dispatcher) claim(
	ctx context.Context,
	agentID agentproto.AgentID,
	request PollRequest,
) (agentproto.PollResponse, bool, error) {
	prepare := func(snapshot repository.RemotePayloadSnapshot) (repository.RemotePreparedClaim, error) {
		if !pollCapabilitiesMatch(request, snapshot) {
			return repository.RemotePreparedClaim{}, errStaleAgentPoll
		}
		return prepareRemoteClaim(snapshot)
	}
	claim, claimed, err := d.repository.ClaimRemoteStepPayload(
		ctx,
		string(agentID),
		prepare,
	)
	if err != nil || claimed {
		return pollResponse(claim), claimed, err
	}
	claim, claimed, err = d.repository.ClaimRemoteDeploymentPayload(
		ctx,
		string(agentID),
		prepare,
	)
	if err != nil || !claimed {
		return agentproto.PollResponse{}, false, err
	}
	return pollResponse(claim), true, nil
}

func pollResponse(claim repository.RemoteClaim) agentproto.PollResponse {
	return agentproto.PollResponse{
		DeploymentID: agentproto.DeploymentID(claim.DeploymentID),
		Payload:      string(claim.Ciphertext),
		ClaimToken:   agentproto.ClaimToken(claim.Token),
	}
}

func (d *Dispatcher) Maintain(ctx context.Context) error {
	err := d.repository.WithQueueMaintenanceTx(
		ctx,
		func(ctx context.Context, q *db.Queries) error {
			now, err := q.CurrentUnixTime(ctx)
			if err != nil {
				return err
			}
			if _, err := q.ExpireAgentPairings(ctx, now); err != nil {
				return err
			}
			if _, err := q.ExpireRemoteStepClaims(ctx, now); err != nil {
				return err
			}
			cancelStaleBefore := now - int64(
				agentproto.CancelAcknowledgementTimeout/time.Second,
			)
			if _, err := q.ExpireRemoteStepCancellations(
				ctx,
				db.ExpireRemoteStepCancellationsParams{
					Now: sql.NullInt64{Int64: now, Valid: true},
					StaleBefore: sql.NullInt64{
						Int64: cancelStaleBefore,
						Valid: true,
					},
				},
			); err != nil {
				return err
			}
			heartbeatStaleBefore := now - int64(
				agentproto.LostThreshold/time.Second,
			)
			if _, err := q.LoseStaleRemoteStepRuns(
				ctx,
				db.LoseStaleRemoteStepRunsParams{
					Now: sql.NullInt64{Int64: now, Valid: true},
					StaleBefore: sql.NullInt64{
						Int64: heartbeatStaleBefore,
						Valid: true,
					},
				},
			); err != nil {
				return err
			}
			_, err = q.FailDeploymentsWithTerminalRemoteStepRuns(
				ctx,
				sql.NullInt64{Int64: now, Valid: true},
			)
			if err != nil {
				return err
			}
			return q.ReconcileTerminalVerifications(ctx)
		},
	)
	if err != nil {
		return fmt.Errorf("maintain agent dispatch: %w", err)
	}
	if err := d.repository.MaintainRemoteLifecycle(ctx); err != nil {
		return fmt.Errorf("maintain remote deployment lifecycle: %w", err)
	}
	return nil
}
