package dispatch

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"

	"github.com/DeveloperDurp/durpdeploy-agent/executor"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

// Dispatcher owns remote dispatch maintenance. Execution is wired separately.
type Dispatcher struct {
	repository *repository.Repository
}

func New(repo *repository.Repository) *Dispatcher {
	return &Dispatcher{repository: repo}
}

func (d *Dispatcher) Poll(
	ctx context.Context,
	agentID agentproto.AgentID,
) (agentproto.PollResponse, bool, error) {
	response, claimed, err := d.claim(ctx, agentID)
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
			response, claimed, err = d.claim(ctx, agentID)
			return response, claimed, err
		}
		response, claimed, err = d.claim(ctx, agentID)
		if err != nil || claimed {
			return response, claimed, err
		}
	}
}

func (d *Dispatcher) claim(
	ctx context.Context,
	agentID agentproto.AgentID,
) (agentproto.PollResponse, bool, error) {
	claim, claimed, err := d.repository.ClaimRemoteStepPayload(
		ctx,
		string(agentID),
		prepareRemoteClaim,
	)
	if err != nil || claimed {
		return pollResponse(claim), claimed, err
	}
	claim, claimed, err = d.repository.ClaimRemoteDeploymentPayload(
		ctx,
		string(agentID),
		prepareRemoteClaim,
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

func prepareRemoteClaim(
	snapshot repository.RemotePayloadSnapshot,
) (repository.RemotePreparedClaim, error) {
	resolved, err := runner.ResolveReleaseVariables(
		snapshot.Variables,
		snapshot.Environment.ID,
	)
	if err != nil {
		return repository.RemotePreparedClaim{}, err
	}
	steps := make([]executor.Step, len(snapshot.Steps))
	for index, step := range snapshot.Steps {
		steps[index] = executor.Step{
			Name:           step.Name,
			ScriptBody:     step.ScriptBody,
			SortOrder:      int64(index + 1),
			TimeoutSeconds: step.TimeoutSeconds,
			MaxRetries:     step.MaxRetries,
		}
	}
	payload := Payload{
		DeploymentID: agentproto.DeploymentID(snapshot.Deployment.ID),
		Release: ReleaseSnapshot{
			ID:        snapshot.Release.ID,
			ProjectID: snapshot.Release.ProjectID,
			Version:   snapshot.Release.Version,
			Steps:     steps,
		},
		Environment: EnvironmentSnapshot{
			ID:   snapshot.Environment.ID,
			Name: snapshot.Environment.Name,
		},
		Variables: VariableSnapshots(resolved),
	}
	certificate, _ := pem.Decode([]byte(snapshot.Agent.CertificatePem.String))
	if certificate == nil || certificate.Type != "CERTIFICATE" {
		return repository.RemotePreparedClaim{}, fmt.Errorf(
			"decode agent certificate",
		)
	}
	ciphertext, err := payload.Seal(certificate.Bytes)
	if err != nil {
		return repository.RemotePreparedClaim{}, err
	}
	rawToken := make([]byte, 32)
	if _, err := rand.Read(rawToken); err != nil {
		return repository.RemotePreparedClaim{}, fmt.Errorf(
			"generate claim token: %w",
			err,
		)
	}
	token := base64.RawURLEncoding.EncodeToString(rawToken)
	tokenHash := sha256.Sum256([]byte(token))
	return repository.RemotePreparedClaim{
		Token: token, TokenHash: tokenHash[:], Ciphertext: ciphertext,
	}, nil
}

func (d *Dispatcher) Maintain(ctx context.Context) error {
	err := d.repository.WithTx(ctx, func(q *db.Queries) error {
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
		return nil
	})
	if err != nil {
		return fmt.Errorf("maintain agent dispatch: %w", err)
	}
	if err := d.repository.MaintainRemoteLifecycle(ctx); err != nil {
		return fmt.Errorf("maintain remote deployment lifecycle: %w", err)
	}
	return nil
}
