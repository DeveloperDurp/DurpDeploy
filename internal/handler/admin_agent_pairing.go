package handler

import (
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/agentpairing"
	"durpdeploy/internal/agentproto"
	"durpdeploy/internal/db"
)

type pendingAgentPairing struct {
	code     agentproto.PairingCode
	endpoint string
	agentPin agentproto.SHA256Pin
	expires  time.Time
}

func (h *AgentAdminHandler) StartPairing(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if h.pairer == nil {
		http.Error(
			writer,
			"agent pairing is unavailable",
			http.StatusServiceUnavailable,
		)
		return
	}
	if err := request.ParseForm(); err != nil {
		http.Error(writer, "invalid pairing form", http.StatusBadRequest)
		return
	}
	endpoint, err := normalizeAgentEndpoint(
		request.FormValue("agent_host"),
		request.FormValue("agent_port"),
	)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	agent, ok := h.agentPageAgent(writer, request)
	if !ok {
		return
	}
	if agent.Status != "pending" {
		http.Error(writer, "agent must be pending", http.StatusConflict)
		return
	}
	pairing, ok := preflightAgentPairing(
		request.Context(),
		endpoint,
		request.FormValue("pairing_code"),
		request.FormValue("agent_fingerprint"),
	)
	if !ok {
		http.Error(
			writer,
			"agent pairing offer is invalid",
			http.StatusUnprocessableEntity,
		)
		return
	}
	initiation := agentPairingInitiation{agent: agent, pairing: pairing}
	confirmation, err := renderAgentPairingConfirmation(
		request.Context(),
		request.URL.Path,
		initiation,
	)
	if err != nil {
		http.Error(
			writer,
			"could not render pairing confirmation",
			http.StatusInternalServerError,
		)
		return
	}
	h.finishPairing(writer, initiation, confirmation)
}

func (h *AgentAdminHandler) ConfirmPairing(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if h.pairer == nil {
		http.Error(
			writer,
			"agent pairing is unavailable",
			http.StatusServiceUnavailable,
		)
		return
	}
	if err := request.ParseForm(); err != nil {
		http.Error(
			writer,
			"invalid pairing confirmation",
			http.StatusBadRequest,
		)
		return
	}
	agentID := chi.URLParam(request, "agentID")
	h.pendingMu.Lock()
	pending, ok := h.pending[agentID]
	h.pendingMu.Unlock()
	if !ok {
		http.Error(writer, "pairing confirmation expired", http.StatusConflict)
		return
	}
	if !time.Now().Before(pending.expires) &&
		!h.canRecoverExpiredConfirmation(request.Context(), agentID, pending) {
		http.Error(writer, "pairing confirmation expired", http.StatusConflict)
		return
	}
	confirmed, err := agentproto.ParseSHA256Pin(request.FormValue("agent_pin"))
	if err != nil || confirmed != pending.agentPin {
		http.Error(
			writer,
			"agent fingerprint does not match",
			http.StatusUnprocessableEntity,
		)
		return
	}
	serverPin, err := agentproto.ParseSHA256Pin(
		h.pairer.Identity.Fingerprint.String(),
	)
	if err != nil {
		http.Error(
			writer,
			"invalid server pairing identity",
			http.StatusInternalServerError,
		)
		return
	}
	pairInput := agentpairing.PairInput{
		Endpoint: pending.endpoint, AgentPin: pending.agentPin,
		Identity: h.pairer.Identity,
		Request: agentproto.PairRequest{
			ProtocolEnvelope: agentproto.ProtocolEnvelope{
				Protocol: agentproto.AgentV1,
			},
			PairingCode: pending.code, AgentPin: pending.agentPin, ServerPin: serverPin,
			PullEndpoint: h.pairer.PullEndpoint, AgentID: agentID,
		},
	}
	hash := pending.code.Hash()
	now := time.Now().Unix()
	err = h.repo.WithTx(request.Context(), func(queries *db.Queries) error {
		_, beginErr := beginAgentPairing(
			request.Context(),
			queries,
			agentID,
			pending,
			"",
		)
		return beginErr
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(writer, "pairing is no longer pending", http.StatusConflict)
			return
		}
		http.Error(writer, "could not begin pairing", http.StatusInternalServerError)
		return
	}
	pairedIdentity, err := agentpairing.Pair(request.Context(), pairInput)
	if err != nil {
		http.Error(
			writer,
			"agent pairing was rejected",
			http.StatusUnprocessableEntity,
		)
		return
	}
	err = h.repo.WithTx(request.Context(), func(queries *db.Queries) error {
		paired, completeErr := queries.CompleteAgentPairing(
			request.Context(),
			db.CompleteAgentPairingParams{
				ServerPublicIdentity: sql.NullString{
					String: certificatePEM(h.pairer), Valid: true,
				},
				AgentPublicIdentity: pairedIdentity.PublicIdentity,
				ServerPin: sql.NullString{
					String: serverPin.String(),
					Valid:  true,
				},
				PairedAt:  sql.NullInt64{Int64: now, Valid: true},
				UpdatedAt: now, AgentID: agentID, PairingCodeHash: hash[:],
				AgentPin: pending.agentPin.String(),
			},
		)
		if completeErr != nil {
			return completeErr
		}
		_, activateErr := queries.ActivatePairedAgent(
			request.Context(),
			db.ActivatePairedAgentParams{
				CertificatePem: sql.NullString{
					String: paired.AgentPublicIdentity,
					Valid:  true,
				},
				CertificateFingerprint: sql.NullString{
					String: pending.agentPin.String(),
					Valid:  true,
				},
				LastHeartbeatAt: sql.NullInt64{Int64: now, Valid: true},
				EnrolledAt:      sql.NullInt64{Int64: now, Valid: true},
				UpdatedAt:       now,
				ID:              agentID,
				PairingCodeHash: hash[:],
			},
		)
		return activateErr
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(
				writer,
				"pairing is no longer pending",
				http.StatusConflict,
			)
			return
		}
		http.Error(
			writer,
			"could not complete pairing",
			http.StatusInternalServerError,
		)
		return
	}
	pairInput.Request.CompletionAck = true
	if _, err := agentpairing.Pair(request.Context(), pairInput); err != nil {
		http.Error(
			writer,
			"agent pairing acknowledgement was rejected",
			http.StatusUnprocessableEntity,
		)
		return
	}
	h.pendingMu.Lock()
	delete(h.pending, agentID)
	h.pendingMu.Unlock()
	writer.Header().Set("Cache-Control", "no-store")
	http.Redirect(
		writer,
		request,
		"/admin/agents/"+agentID,
		http.StatusSeeOther,
	)
}
