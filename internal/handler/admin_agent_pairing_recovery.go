package handler

import (
	"bytes"
	"context"
	"encoding/pem"

	"durpdeploy/internal/agentpairing"
)

func (h *AgentAdminHandler) canRecoverExpiredConfirmation(
	ctx context.Context,
	agentID string,
	pending pendingAgentPairing,
) bool {
	hash := pending.code.Hash()
	pairing, err := h.repo.Queries.GetAgentPairing(ctx, agentID)
	if err != nil || !bytes.Equal(pairing.PairingCodeHash, hash[:]) ||
		pairing.AgentPin != pending.agentPin.String() {
		return false
	}
	switch pairing.State {
	case "committing":
		return true
	case "paired":
		return pairing.ServerPin.Valid &&
			pairing.ServerPin.String == h.pairer.Identity.Fingerprint.String()
	default:
		return false
	}
}

func certificatePEM(pairer *agentpairing.Server) string {
	return string(pem.EncodeToMemory(&pem.Block{
		Type: "CERTIFICATE", Bytes: pairer.Identity.Certificate.Certificate[0],
	}))
}
