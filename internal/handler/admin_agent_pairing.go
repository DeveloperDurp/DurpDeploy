package handler

import (
	"database/sql"
	"encoding/pem"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/agentpairing"
	"durpdeploy/internal/agentproto"
	"durpdeploy/internal/db"
	"durpdeploy/views/pages"
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
	code, err := agentproto.ParsePairingCode(request.FormValue("pairing_code"))
	if err != nil {
		http.Error(
			writer,
			"invalid pairing code",
			http.StatusUnprocessableEntity,
		)
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
	bootstrap, err := agentpairing.FetchBootstrapIdentity(
		request.Context(),
		request.FormValue("agent_endpoint"),
	)
	if err != nil || bootstrap.Offer.PairingCode.Hash() != code.Hash() ||
		!time.Now().Before(bootstrap.Offer.ExpiresAt) {
		http.Error(
			writer,
			"agent pairing code is invalid",
			http.StatusUnprocessableEntity,
		)
		return
	}
	hash := code.Hash()
	if _, err := h.repo.Queries.CreateAgentPairing(
		request.Context(),
		db.CreateAgentPairingParams{
			AgentID:             agent.ID,
			PairingCodeHash:     hash[:],
			AgentPublicIdentity: bootstrap.PublicIdentity,
			AgentPin:            bootstrap.Offer.AgentPin.String(),
			ExpiresAt:           bootstrap.Offer.ExpiresAt.Unix(),
		},
	); err != nil {
		if IsUniqueViolation(err) {
			http.Error(
				writer,
				"agent pairing already exists",
				http.StatusConflict,
			)
			return
		}
		http.Error(
			writer,
			"could not store pending pairing",
			http.StatusInternalServerError,
		)
		return
	}
	h.pendingMu.Lock()
	h.pending[agent.ID] = pendingAgentPairing{
		code: code, endpoint: request.FormValue("agent_endpoint"),
		agentPin: bootstrap.Offer.AgentPin, expires: bootstrap.Offer.ExpiresAt,
	}
	h.pendingMu.Unlock()
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusCreated)
	if err := pages.AgentPairingConfirmationPage(pages.AgentPairingConfirmationView{
		Agent: agent, AgentPin: bootstrap.Offer.AgentPin.String(),
		CurrentPath: request.URL.Path,
	}).
		Render(request.Context(), writer); err != nil {
		http.Error(
			writer,
			"could not render pairing confirmation",
			http.StatusInternalServerError,
		)
	}
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
	if !ok || !time.Now().Before(pending.expires) {
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
			PairingCode: pending.code, ServerPin: serverPin,
			PullEndpoint: h.pairer.PullEndpoint, AgentID: agentID,
		},
	}
	_, err = agentpairing.Pair(request.Context(), pairInput)
	if err != nil {
		http.Error(
			writer,
			"agent pairing was rejected",
			http.StatusUnprocessableEntity,
		)
		return
	}
	hash := pending.code.Hash()
	now := time.Now().Unix()
	err = h.repo.WithTx(request.Context(), func(queries *db.Queries) error {
		paired, completeErr := queries.CompleteAgentPairing(
			request.Context(),
			db.CompleteAgentPairingParams{
				ServerPublicIdentity: sql.NullString{
					String: certificatePEM(h.pairer), Valid: true,
				},
				ServerPin: sql.NullString{
					String: serverPin.String(),
					Valid:  true,
				},
				PairedAt:  sql.NullInt64{Int64: now, Valid: true},
				UpdatedAt: now, AgentID: agentID, PairingCodeHash: hash[:],
				AgentPin: pending.agentPin.String(), Now: now,
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
	if err := agentpairing.Commit(request.Context(), pairInput); err != nil {
		http.Error(writer, "agent pairing commit was rejected", http.StatusInternalServerError)
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

func certificatePEM(pairer *agentpairing.Server) string {
	return string(pem.EncodeToMemory(&pem.Block{
		Type: "CERTIFICATE", Bytes: pairer.Identity.Certificate.Certificate[0],
	}))
}
