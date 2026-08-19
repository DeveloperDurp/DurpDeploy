package agentserver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"durpdeploy/internal/agentproto"
	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
)

const remoteLostReason = "remote agent heartbeat expired"

// Maintain applies one durable, server-clock maintenance pass.
func (server *Server) Maintain(ctx context.Context) error {
	now := server.now().Unix()
	notifications := make([]events.Event, 0)
	err := server.repository.WithTx(ctx, func(queries *db.Queries) error {
		if err := recordOfflineAgents(ctx, queries, now); err != nil {
			return err
		}
		if err := reclaimExpiredClaims(ctx, queries, now); err != nil {
			return err
		}
		if err := expireCancellations(ctx, queries, now); err != nil {
			return err
		}
		lost, err := loseStartedDeployments(ctx, queries, now)
		if err != nil {
			return err
		}
		notifications = lost
		return nil
	})
	if err != nil {
		return fmt.Errorf("maintain remote agent dispatches: %w", err)
	}
	if server.events != nil {
		for _, notification := range notifications {
			server.events.Publish(ctx, notification)
		}
	}
	return nil
}

func recordOfflineAgents(
	ctx context.Context,
	queries *db.Queries,
	now int64,
) error {
	before := sql.NullInt64{
		Int64: now - int64(agentproto.LostThreshold.Seconds()),
		Valid: true,
	}
	agentIDs, err := queries.ListOfflineAgentIDs(ctx, before)
	if err != nil {
		return fmt.Errorf("list offline agents: %w", err)
	}
	for _, agentID := range agentIDs {
		if _, err := queries.CreateOfflineAgentEvent(
			ctx,
			db.CreateOfflineAgentEventParams{
				AgentID:             agentID,
				LastHeartbeatBefore: before,
			},
		); err != nil {
			return fmt.Errorf("record offline agent: %w", err)
		}
	}
	return nil
}

func reclaimExpiredClaims(
	ctx context.Context,
	queries *db.Queries,
	now int64,
) error {
	before := sql.NullInt64{Int64: now, Valid: true}
	dispatches, err := queries.ListExpiredClaimedDeploymentDispatches(
		ctx,
		before,
	)
	if err != nil {
		return fmt.Errorf("list expired claims: %w", err)
	}
	for _, dispatch := range dispatches {
		if !dispatch.AgentID.Valid {
			return errors.New("claimed dispatch missing agent")
		}
		_, err := queries.ReclaimExpiredClaimedDeploymentDispatch(
			ctx,
			db.ReclaimExpiredClaimedDeploymentDispatchParams{
				UpdatedAt:          now,
				DeploymentID:       dispatch.DeploymentID,
				AgentID:            dispatch.AgentID,
				ClaimTokenHash:     dispatch.ClaimTokenHash,
				ClaimExpiresBefore: before,
			},
		)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return fmt.Errorf("reclaim expired claim: %w", err)
		}
		if _, err := queries.CreateAgentEvent(
			ctx,
			db.CreateAgentEventParams{
				AgentID: dispatch.AgentID,
				DeploymentID: sql.NullInt64{
					Int64: dispatch.DeploymentID,
					Valid: true,
				},
				EventType: "claim_reclaimed",
				DispatchState: sql.NullString{
					String: string(agentproto.DispatchWaiting),
					Valid:  true,
				},
			},
		); err != nil {
			return fmt.Errorf("record reclaimed claim: %w", err)
		}
	}
	return nil
}

func expireCancellations(
	ctx context.Context,
	queries *db.Queries,
	now int64,
) error {
	before := sql.NullInt64{
		Int64: now - int64(agentproto.CancelAcknowledgementTimeout.Seconds()),
		Valid: true,
	}
	dispatches, err := queries.ListExpiredCancellationDeploymentDispatches(
		ctx,
		before,
	)
	if err != nil {
		return fmt.Errorf("list expired cancellations: %w", err)
	}
	for _, dispatch := range dispatches {
		if !dispatch.AgentID.Valid {
			return errors.New("cancellation dispatch missing agent")
		}
		_, err := queries.TransitionDeploymentDispatch(
			ctx,
			db.TransitionDeploymentDispatchParams{
				NextState:      string(agentproto.DispatchCancelUnconfirmed),
				FinishedAt:     sql.NullInt64{},
				DeploymentID:   dispatch.DeploymentID,
				AgentID:        dispatch.AgentID,
				ClaimTokenHash: dispatch.ClaimTokenHash,
				CurrentState:   string(agentproto.DispatchCancelRequested),
			},
		)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return fmt.Errorf("expire cancellation: %w", err)
		}
		if _, err := queries.CreateAgentEvent(
			ctx,
			db.CreateAgentEventParams{
				AgentID: dispatch.AgentID,
				DeploymentID: sql.NullInt64{
					Int64: dispatch.DeploymentID,
					Valid: true,
				},
				EventType: "cancel_unconfirmed",
				DispatchState: sql.NullString{
					String: string(agentproto.DispatchCancelUnconfirmed),
					Valid:  true,
				},
			},
		); err != nil {
			return fmt.Errorf("record unconfirmed cancellation: %w", err)
		}
	}
	return nil
}

func loseStartedDeployments(
	ctx context.Context,
	queries *db.Queries,
	now int64,
) ([]events.Event, error) {
	before := sql.NullInt64{
		Int64: now - int64(agentproto.LostThreshold.Seconds()),
		Valid: true,
	}
	dispatches, err := queries.ListLostStartedDeploymentDispatches(ctx, before)
	if err != nil {
		return nil, fmt.Errorf("list lost deployments: %w", err)
	}
	notifications := make([]events.Event, 0, len(dispatches))
	for _, dispatch := range dispatches {
		if !dispatch.AgentID.Valid {
			return nil, errors.New("started dispatch missing agent")
		}
		_, err := queries.TransitionDeploymentDispatch(
			ctx,
			db.TransitionDeploymentDispatchParams{
				NextState: string(agentproto.DispatchLost),
				Reason: sql.NullString{
					String: remoteLostReason,
					Valid:  true,
				},
				FinishedAt:     sql.NullInt64{Int64: now, Valid: true},
				DeploymentID:   dispatch.DeploymentID,
				AgentID:        dispatch.AgentID,
				ClaimTokenHash: dispatch.ClaimTokenHash,
				CurrentState:   string(agentproto.DispatchStarted),
			},
		)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("mark deployment lost: %w", err)
		}
		if err := queries.UpdateDeploymentStatus(
			ctx,
			db.UpdateDeploymentStatusParams{
				ID:         dispatch.DeploymentID,
				Status:     "failed",
				FinishedAt: sql.NullInt64{Int64: now, Valid: true},
			},
		); err != nil {
			return nil, fmt.Errorf("mark lost deployment failed: %w", err)
		}
		if _, err := queries.CreateAgentEvent(
			ctx,
			db.CreateAgentEventParams{
				AgentID: dispatch.AgentID,
				DeploymentID: sql.NullInt64{
					Int64: dispatch.DeploymentID,
					Valid: true,
				},
				EventType: "deployment_lost",
				DispatchState: sql.NullString{
					String: string(agentproto.DispatchLost),
					Valid:  true,
				},
			},
		); err != nil {
			return nil, fmt.Errorf("record lost deployment: %w", err)
		}
		notification, err := lostNotification(
			ctx,
			queries,
			dispatch.DeploymentID,
		)
		if err != nil {
			return nil, err
		}
		notifications = append(notifications, notification)
	}
	return notifications, nil
}

func lostNotification(
	ctx context.Context,
	queries *db.Queries,
	deploymentID int64,
) (events.Event, error) {
	deployment, err := queries.GetDeployment(ctx, deploymentID)
	if err != nil {
		return events.Event{}, fmt.Errorf("get lost deployment: %w", err)
	}
	release, err := queries.GetRelease(ctx, deployment.ReleaseID)
	if err != nil {
		return events.Event{}, fmt.Errorf(
			"get lost deployment release: %w",
			err,
		)
	}
	return events.Event{
		Type:          events.DeploymentFailed,
		DeploymentID:  deploymentID,
		ProjectID:     release.ProjectID,
		EnvironmentID: deployment.EnvironmentID,
		Message:       "Remote deployment lost contact with its agent",
	}, nil
}
