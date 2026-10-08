package agentserver

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func (service *PairingService) cleanup(
	ctx context.Context,
	connection pairingConnection,
	request agentproto.PairRequest,
	result PairingResult,
) (PairingResult, error) {
	status, err := connection.Post(ctx, request)
	if err != nil {
		slog.Warn(
			"paired agent cleanup acknowledgement failed",
			"agent_id", result.AgentID,
			"err", err,
		)
	} else if status != http.StatusNoContent {
		slog.Warn(
			"paired agent cleanup acknowledgement rejected",
			"agent_id", result.AgentID,
			"status", status,
		)
	}
	// Completion can race deletion, revocation or replacement while waiting
	// on the agent. Verify the live tuple after that network round trip.
	// Activation already committed; an expired ACK must not skip this check.
	checkCtx, cancel := context.WithTimeout(
		context.WithoutCancel(ctx),
		5*time.Second,
	)
	defer cancel()
	pairing, err := service.repository.Queries.GetRegisteredAgentPairing(
		checkCtx,
		result.AgentID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return result, ErrPairingConflict
	}
	if err != nil {
		return result, fmt.Errorf("confirm pairing registration: %w", err)
	}
	codeHash := request.PairingCode.Hash()
	if pairing.AgentPin != request.AgentPin.String() ||
		pairing.ServerPin.String != request.ServerPin.String() ||
		pairing.ServerPullEndpoint.String != request.PullEndpoint.String() ||
		subtle.ConstantTimeCompare(pairing.PairingCodeHash, codeHash[:]) != 1 {
		return result, ErrPairingConflict
	}
	return result, nil
}
