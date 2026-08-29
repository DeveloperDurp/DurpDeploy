package handler

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/views/pages"
)

var errBootstrapOfferInUse = errors.New(
	"bootstrap offer is already being used by another agent",
)

func beginAgentPairing(
	ctx context.Context,
	queries *db.Queries,
	agentID string,
	pending pendingAgentPairing,
	agentPublicIdentity string,
) (db.AgentPairing, error) {
	hash := pending.code.Hash()
	now := time.Now().Unix()
	pairing, err := queries.CreateAgentPairing(
		ctx,
		db.CreateAgentPairingParams{
			AgentID:             agentID,
			PairingCodeHash:     hash[:],
			AgentPublicIdentity: agentPublicIdentity,
			AgentPin:            pending.agentPin.String(),
			ExpiresAt:           time.Now().Add(10 * time.Minute).Unix(),
		},
	)
	if IsUniqueViolation(err) {
		return db.AgentPairing{}, errBootstrapOfferInUse
	}
	if errors.Is(err, sql.ErrNoRows) {
		pairing, err = queries.GetAgentPairing(ctx, agentID)
		if err != nil {
			return db.AgentPairing{}, err
		}
		if !bytes.Equal(pairing.PairingCodeHash, hash[:]) ||
			pairing.AgentPin != pending.agentPin.String() {
			return db.AgentPairing{}, sql.ErrNoRows
		}
	} else if err != nil {
		return db.AgentPairing{}, err
	}
	return queries.BeginAgentPairing(ctx, db.BeginAgentPairingParams{
		AgentID: agentID, PairingCodeHash: hash[:], AgentPin: pending.agentPin.String(),
		UpdatedAt: now, Now: now,
	})
}

func renderAgentPairingConfirmation(
	ctx context.Context,
	currentPath string,
	initiation agentPairingInitiation,
) ([]byte, error) {
	var output bytes.Buffer
	if err := pages.AgentPairingConfirmationPage(
		pages.AgentPairingConfirmationView{
			Agent:       initiation.agent,
			AgentPin:    initiation.pairing.bootstrap.AgentPin.String(),
			CurrentPath: currentPath,
		},
	).Render(ctx, &output); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func (h *AgentAdminHandler) finishPairing(
	writer http.ResponseWriter,
	initiation agentPairingInitiation,
	confirmation []byte,
) {
	h.pendingMu.Lock()
	h.pending[initiation.agent.ID] = pendingAgentPairing{
		code: initiation.pairing.code, endpoint: initiation.pairing.endpoint,
		agentPin: initiation.pairing.bootstrap.AgentPin,
		expires:  time.Now().Add(h.confirmationTTL),
	}
	h.pendingMu.Unlock()
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusCreated)
	if _, err := writer.Write(confirmation); err != nil {
		h.pendingMu.Lock()
		delete(h.pending, initiation.agent.ID)
		h.pendingMu.Unlock()
	}
}
