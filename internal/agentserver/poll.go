package agentserver

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"durpdeploy/internal/agentpayload"
	"durpdeploy/internal/agentproto"
	"durpdeploy/internal/db"
)

var errInactiveAgent = errors.New("agent is no longer active")

func (server *Server) poll(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if request.Method != http.MethodPost {
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	decoded, err := agentproto.DecodeRequest[agentproto.PollRequest](
		request.Body,
	)
	if err != nil {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	agent := agentFromContext(request.Context())
	claimed, err := server.claim(request.Context(), agent, decoded.AgentVersion)
	if errors.Is(err, errInactiveAgent) {
		writer.WriteHeader(http.StatusUnauthorized)
		return
	}
	if err != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	if claimed == nil {
		if err := server.pollWait(request.Context()); err != nil {
			return
		}
		claimed, err = server.claimWaiting(request.Context(), agent)
		if errors.Is(err, errInactiveAgent) {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		if err != nil {
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
	}
	if claimed == nil {
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(*claimed); err != nil {
		return
	}
}

func (server *Server) claim(
	ctx context.Context,
	agent db.GetActiveAgentByFingerprintRow,
	version agentproto.AgentVersion,
) (*agentproto.PollResponse, error) {
	now := server.now().Unix()
	if err := server.repository.WithTx(ctx, func(queries *db.Queries) error {
		updated, err := queries.UpdateAgentHeartbeat(
			ctx,
			db.UpdateAgentHeartbeatParams{
				AgentVersion: sql.NullString{
					String: string(version),
					Valid:  true,
				},
				LastHeartbeatAt: sql.NullInt64{Int64: now, Valid: true},
				UpdatedAt:       now,
				ID:              agent.ID,
			},
		)
		if err != nil {
			return err
		}
		if updated != 1 {
			return errInactiveAgent
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return server.claimWaiting(ctx, agent)
}

func (server *Server) claimWaiting(
	ctx context.Context,
	agent db.GetActiveAgentByFingerprintRow,
) (*agentproto.PollResponse, error) {
	claimToken, err := newClaimToken()
	if err != nil {
		return nil, err
	}
	now := server.now().Unix()
	var response *agentproto.PollResponse
	err = server.repository.WithTx(ctx, func(queries *db.Queries) error {
		hash := sha256.Sum256([]byte(claimToken))
		dispatch, err := queries.ClaimOldestDirectDeployment(ctx,
			db.ClaimOldestDirectDeploymentParams{
				AgentID:        sql.NullString{String: agent.ID, Valid: true},
				ClaimTokenHash: hash[:],
				ClaimExpiresAt: sql.NullInt64{
					Int64: now + int64(
						agentproto.PreStartClaimTimeout/time.Second,
					),
					Valid: true,
				},
				LastHeartbeatAt: sql.NullInt64{Int64: now, Valid: true},
			},
		)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		payload, err := queries.GetDeploymentPayload(ctx, dispatch.DeploymentID)
		if err != nil {
			return err
		}
		plaintext, err := server.box.Decrypt(payload.Ciphertext)
		if err != nil {
			return err
		}
		certificate, err := parseAgentCertificate(
			agent.CertificatePem.String,
			server.now(),
		)
		if err != nil {
			return err
		}
		envelope, err := agentpayload.Seal(
			certificate.Raw,
			payload.DeploymentID,
			[]byte(plaintext),
		)
		if err != nil {
			return err
		}
		response = &agentproto.PollResponse{
			DeploymentID: agentproto.DeploymentID(dispatch.DeploymentID),
			Payload:      string(envelope),
			ClaimToken:   agentproto.ClaimToken(claimToken),
		}
		return nil
	})
	return response, err
}

func newClaimToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func waitForPoll(ctx context.Context) error {
	timer := time.NewTimer(agentproto.PollInterval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
