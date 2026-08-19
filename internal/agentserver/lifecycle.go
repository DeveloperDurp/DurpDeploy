package agentserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"net/http"

	"durpdeploy/internal/agentproto"
	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
)

const remoteFailureReason = "remote agent reported failure"

var errLifecycleConflict = errors.New("deployment lifecycle conflict")

func (server *Server) startDeployment(
	ctx context.Context,
	deploymentID int64,
	agentID string,
	token agentproto.ClaimToken,
) error {
	return server.repository.WithTx(ctx, func(queries *db.Queries) error {
		dispatch, hash, err := lifecycleDispatch(
			ctx,
			queries,
			deploymentID,
			agentID,
			token,
		)
		if err != nil {
			return err
		}
		if dispatch.State == string(agentproto.DispatchStarted) {
			return nil
		}
		if dispatch.State != string(agentproto.DispatchClaimed) {
			return errLifecycleConflict
		}
		now := server.now().Unix()
		if _, err := queries.StartClaimedDeploymentDispatch(
			ctx,
			db.StartClaimedDeploymentDispatchParams{
				StartedAt: sql.NullInt64{
					Int64: now,
					Valid: true,
				}, LastHeartbeatAt: sql.NullInt64{Int64: now, Valid: true},
				DeploymentID: deploymentID, AgentID: sql.NullString{String: agentID, Valid: true}, ClaimTokenHash: hash,
			},
		); err != nil {
			return errLifecycleConflict
		}
		return queries.UpdateDeploymentStatus(
			ctx,
			db.UpdateDeploymentStatusParams{
				ID:        deploymentID,
				Status:    "running",
				StartedAt: sql.NullInt64{Int64: now, Valid: true},
			},
		)
	})
}

func (server *Server) heartbeatDeployment(
	ctx context.Context,
	deploymentID int64,
	agentID string,
	token agentproto.ClaimToken,
) (bool, error) {
	cancelRequested := false
	err := server.repository.WithTx(ctx, func(queries *db.Queries) error {
		dispatch, hash, err := lifecycleDispatch(
			ctx,
			queries,
			deploymentID,
			agentID,
			token,
		)
		if err != nil {
			return err
		}
		now := server.now().Unix()
		updated, err := queries.TouchAgentHeartbeat(
			ctx,
			db.TouchAgentHeartbeatParams{
				LastHeartbeatAt: sql.NullInt64{Int64: now, Valid: true},
				UpdatedAt:       now,
				ID:              agentID,
			},
		)
		if err != nil || updated != 1 {
			return errLifecycleConflict
		}
		switch agentproto.DispatchState(dispatch.State) {
		case agentproto.DispatchStarted:
			_, err = queries.RenewDeploymentDispatchClaim(
				ctx,
				db.RenewDeploymentDispatchClaimParams{
					LastHeartbeatAt: sql.NullInt64{Int64: now, Valid: true},
					DeploymentID:    deploymentID,
					AgentID: sql.NullString{
						String: agentID,
						Valid:  true,
					},
					ClaimTokenHash: hash,
					CurrentState:   dispatch.State,
				},
			)
			return err
		case agentproto.DispatchCancelRequested:
			cancelRequested = true
			return nil
		default:
			return errLifecycleConflict
		}
	})
	return cancelRequested, err
}

func (server *Server) writeLogs(
	ctx context.Context,
	deploymentID int64,
	agentID string,
	request agentproto.LogBatchRequest,
) ([]int64, error) {
	logIDs := make([]int64, 0, len(request.Events))
	err := server.repository.WithTx(ctx, func(queries *db.Queries) error {
		dispatch, hash, err := lifecycleDispatch(
			ctx,
			queries,
			deploymentID,
			agentID,
			request.ClaimToken,
		)
		if err != nil {
			return err
		}
		if dispatch.State != string(agentproto.DispatchStarted) {
			return errLifecycleConflict
		}
		for _, event := range request.Events {
			log, err := queries.CreateSequencedDeploymentLogForDispatch(
				ctx,
				db.CreateSequencedDeploymentLogForDispatchParams{
					DeploymentID: deploymentID,
					Line:         event.Line,
					Sequence:     int64(event.Sequence),
					AgentID: sql.NullString{
						String: agentID,
						Valid:  true,
					},
					ClaimTokenHash: hash,
					CurrentState:   dispatch.State,
				},
			)
			if err != nil {
				return errLifecycleConflict
			}
			logIDs = append(logIDs, log.ID)
		}
		return nil
	})
	return logIDs, err
}

func (server *Server) completeDeployment(
	ctx context.Context,
	deploymentID int64,
	agentID string,
	request agentproto.ResultRequest,
) error {
	var notification events.Event
	err := server.repository.WithTx(ctx, func(queries *db.Queries) error {
		dispatch, hash, err := lifecycleDispatch(
			ctx,
			queries,
			deploymentID,
			agentID,
			request.ClaimToken,
		)
		if err != nil {
			return err
		}
		if dispatch.State != string(agentproto.DispatchStarted) {
			return errLifecycleConflict
		}
		nextState, status, eventType := resultTransition(request.State)
		message := "Remote deployment succeeded"
		if eventType == events.DeploymentFailed {
			message = "Remote deployment failed"
		}
		reason := sql.NullString{}
		if request.State == agentproto.ResultFailed {
			reason = sql.NullString{String: remoteFailureReason, Valid: true}
		}
		now := server.now().Unix()
		if _, err := queries.TransitionDeploymentDispatch(
			ctx,
			db.TransitionDeploymentDispatchParams{
				NextState:      string(nextState),
				Reason:         reason,
				FinishedAt:     sql.NullInt64{Int64: now, Valid: true},
				DeploymentID:   deploymentID,
				AgentID:        sql.NullString{String: agentID, Valid: true},
				ClaimTokenHash: hash,
				CurrentState:   dispatch.State,
			},
		); err != nil {
			return errLifecycleConflict
		}
		if err := queries.UpdateDeploymentStatus(
			ctx,
			db.UpdateDeploymentStatusParams{
				ID:         deploymentID,
				Status:     status,
				FinishedAt: sql.NullInt64{Int64: now, Valid: true},
			},
		); err != nil {
			return err
		}
		deployment, err := queries.GetDeployment(ctx, deploymentID)
		if err != nil {
			return err
		}
		release, err := queries.GetRelease(ctx, deployment.ReleaseID)
		if err != nil {
			return err
		}
		notification = events.Event{
			Type:          eventType,
			DeploymentID:  deploymentID,
			ProjectID:     release.ProjectID,
			EnvironmentID: deployment.EnvironmentID,
			Message:       message,
		}
		return nil
	})
	if err == nil && server.events != nil {
		server.events.Publish(ctx, notification)
	}
	return err
}

func (server *Server) cancelDeployment(
	ctx context.Context,
	deploymentID int64,
	agentID string,
	token agentproto.ClaimToken,
) error {
	return server.repository.WithTx(ctx, func(queries *db.Queries) error {
		dispatch, hash, err := lifecycleDispatch(
			ctx,
			queries,
			deploymentID,
			agentID,
			token,
		)
		if err != nil {
			return err
		}
		if dispatch.State == string(agentproto.DispatchCancelled) {
			return nil
		}
		if dispatch.State != string(agentproto.DispatchCancelRequested) {
			return errLifecycleConflict
		}
		now := server.now().Unix()
		if _, err := queries.AcknowledgeDeploymentDispatchCancellation(
			ctx,
			db.AcknowledgeDeploymentDispatchCancellationParams{
				FinishedAt:     sql.NullInt64{Int64: now, Valid: true},
				DeploymentID:   deploymentID,
				AgentID:        sql.NullString{String: agentID, Valid: true},
				ClaimTokenHash: hash,
			},
		); err != nil {
			return errLifecycleConflict
		}
		return queries.UpdateDeploymentStatus(
			ctx,
			db.UpdateDeploymentStatusParams{
				ID:         deploymentID,
				Status:     "cancelled",
				FinishedAt: sql.NullInt64{Int64: now, Valid: true},
			},
		)
	})
}

func lifecycleDispatch(
	ctx context.Context,
	queries *db.Queries,
	deploymentID int64,
	agentID string,
	token agentproto.ClaimToken,
) (db.DeploymentDispatch, []byte, error) {
	hash := sha256.Sum256([]byte(token))
	dispatch, err := queries.GetDeploymentDispatch(ctx, deploymentID)
	if err != nil || !dispatch.AgentID.Valid ||
		dispatch.AgentID.String != agentID ||
		!bytes.Equal(dispatch.ClaimTokenHash, hash[:]) {
		return db.DeploymentDispatch{}, nil, errLifecycleConflict
	}
	return dispatch, hash[:], nil
}

func (server *Server) recordLifecycleConflict(
	ctx context.Context,
	deploymentID int64,
	agentID string,
	token agentproto.ClaimToken,
	err error,
) {
	if !errors.Is(err, errLifecycleConflict) {
		return
	}
	dispatch, err := server.repository.Queries.GetDeploymentDispatch(
		ctx,
		deploymentID,
	)
	if err != nil {
		return
	}
	hash := sha256.Sum256([]byte(token))
	_, _ = server.repository.Queries.CreateAgentEventForDispatch(
		ctx,
		db.CreateAgentEventForDispatchParams{
			AgentID:        sql.NullString{String: agentID, Valid: true},
			DeploymentID:   sql.NullInt64{Int64: deploymentID, Valid: true},
			EventType:      "stale_mutation",
			CurrentState:   sql.NullString{String: dispatch.State, Valid: true},
			ClaimTokenHash: hash[:],
		},
	)
}

func (server *Server) recordLateResult(
	ctx context.Context,
	deploymentID int64,
	agentID string,
	token agentproto.ClaimToken,
) bool {
	dispatch, err := server.repository.Queries.GetDeploymentDispatch(
		ctx,
		deploymentID,
	)
	if err != nil ||
		(dispatch.State != string(agentproto.DispatchLost) && dispatch.State != string(agentproto.DispatchCancelUnconfirmed)) {
		return false
	}
	hash := sha256.Sum256([]byte(token))
	_, err = server.repository.Queries.CreateAgentEventForDispatch(
		ctx,
		db.CreateAgentEventForDispatchParams{
			AgentID:        sql.NullString{String: agentID, Valid: true},
			DeploymentID:   sql.NullInt64{Int64: deploymentID, Valid: true},
			EventType:      "late_result",
			CurrentState:   sql.NullString{String: dispatch.State, Valid: true},
			ClaimTokenHash: hash[:],
		},
	)
	return err == nil
}

func resultTransition(
	state agentproto.ResultState,
) (agentproto.DispatchState, string, events.Type) {
	switch state {
	case agentproto.ResultSucceeded:
		return agentproto.DispatchSucceeded, "succeeded", events.DeploymentSucceeded
	case agentproto.ResultFailed:
		return agentproto.DispatchFailed, "failed", events.DeploymentFailed
	default:
		return "", "", ""
	}
}

func (server *Server) writeLifecycleError(
	writer http.ResponseWriter,
	err error,
) {
	if errors.Is(err, errLifecycleConflict) {
		writer.WriteHeader(http.StatusConflict)
		return
	}
	writer.WriteHeader(http.StatusInternalServerError)
}
