package agentserver

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"

	"github.com/go-chi/chi/v5"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
)

func (s *Server) Poll(w http.ResponseWriter, r *http.Request) {
	request, ok := decodeRequest[agentproto.PollRequest](w, r)
	if !ok {
		return
	}
	agentID, _ := AgentIDFromContext(r.Context())
	fingerprint := agenttls.FingerprintOf(
		r.TLS.PeerCertificates[0].Raw,
	).String()
	changed, err := s.repository.Queries.HeartbeatAgent(
		r.Context(),
		db.HeartbeatAgentParams{
			Now: nullableInt64(time.Now().Unix()),
			AgentVersion: nullableString(
				string(request.AgentVersion),
			),
			ID:                     string(agentID),
			CertificateFingerprint: nullableString(fingerprint),
		},
	)
	if !writeTransitionStatus(w, changed, err) {
		return
	}
	response, claimed, err := s.dispatcher.Poll(r.Context(), agentID)
	if err != nil {
		if r.Context().Err() == nil {
			w.WriteHeader(http.StatusInternalServerError)
		}
		return
	}
	if !claimed {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, response)
}

func (s *Server) Start(w http.ResponseWriter, r *http.Request) {
	request, ok := decodeRequest[agentproto.StartRequest](w, r)
	if !ok {
		return
	}
	deploymentID, ok := deploymentID(w, r)
	if !ok {
		return
	}
	agentID, _ := AgentIDFromContext(r.Context())
	err := s.repository.StartRemoteDeployment(
		r.Context(),
		repository.RemoteLifecycleClaim{
			DeploymentID:   deploymentID,
			AgentID:        string(agentID),
			ClaimTokenHash: claimTokenHash(request.ClaimToken),
		},
	)
	if writeLifecycleStatus(w, err) {
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) Heartbeat(w http.ResponseWriter, r *http.Request) {
	request, ok := decodeRequest[agentproto.HeartbeatRequest](w, r)
	if !ok {
		return
	}
	deploymentID, ok := deploymentID(w, r)
	if !ok {
		return
	}
	agentID, _ := AgentIDFromContext(r.Context())
	result, err := s.repository.HeartbeatRemoteDeployment(
		r.Context(),
		repository.RemoteLifecycleClaim{
			DeploymentID:   deploymentID,
			AgentID:        string(agentID),
			ClaimTokenHash: claimTokenHash(request.ClaimToken),
		},
	)
	if !writeLifecycleStatus(w, err) {
		return
	}
	writeJSON(w, agentproto.HeartbeatResponse{
		CancelRequested: result.CancelRequested,
		ServerPins: []agentproto.CertificateFingerprint{
			agentproto.CertificateFingerprint(s.identity.Fingerprint.String()),
		},
	})
}

func (s *Server) Logs(w http.ResponseWriter, r *http.Request) {
	request, ok := decodeLogBatch(w, r)
	if !ok {
		return
	}
	deploymentID, ok := deploymentID(w, r)
	if !ok {
		return
	}
	agentID, _ := AgentIDFromContext(r.Context())
	claim := db.LockRemoteDeploymentClaimParams{
		DeploymentID:   deploymentID,
		AgentID:        string(agentID),
		ClaimTokenHash: claimTokenHash(request.ClaimToken),
	}
	changed, err := s.repository.Queries.LockRemoteDeploymentClaim(
		r.Context(),
		claim,
	)
	if !writeTransitionStatus(w, changed, err) {
		return
	}
	for _, event := range request.Events {
		_, err := s.repository.AppendRemoteDeploymentLog(
			r.Context(),
			repository.RemoteDeploymentLog{
				LockRemoteDeploymentClaimParams: claim,
				Sequence:                        int64(event.Sequence),
				Line:                            event.Line,
			},
		)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) Result(w http.ResponseWriter, r *http.Request) {
	request, ok := decodeRequest[agentproto.ResultRequest](w, r)
	if !ok {
		return
	}
	deploymentID, ok := deploymentID(w, r)
	if !ok {
		return
	}
	agentID, _ := AgentIDFromContext(r.Context())
	err := s.repository.FinishRemoteDeploymentLifecycle(
		r.Context(),
		repository.RemoteLifecycleClaim{
			DeploymentID:   deploymentID,
			AgentID:        string(agentID),
			ClaimTokenHash: claimTokenHash(request.ClaimToken),
		},
		string(request.State),
		nullableString(request.Error),
	)
	if writeLifecycleStatus(w, err) {
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) Cancelled(w http.ResponseWriter, r *http.Request) {
	request, ok := decodeRequest[agentproto.CancelledRequest](w, r)
	if !ok {
		return
	}
	deploymentID, ok := deploymentID(w, r)
	if !ok {
		return
	}
	agentID, _ := AgentIDFromContext(r.Context())
	err := s.repository.AcknowledgeRemoteCancellation(
		r.Context(),
		repository.RemoteLifecycleClaim{
			DeploymentID:   deploymentID,
			AgentID:        string(agentID),
			ClaimTokenHash: claimTokenHash(request.ClaimToken),
		},
	)
	if writeLifecycleStatus(w, err) {
		w.WriteHeader(http.StatusNoContent)
	}
}

func deploymentID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id < 1 {
		w.WriteHeader(http.StatusConflict)
		return 0, false
	}
	return id, true
}

func claimTokenHash(token agentproto.ClaimToken) []byte {
	hash := sha256.Sum256([]byte(token))
	return hash[:]
}

func nullableInt64(value int64) sql.NullInt64 {
	return sql.NullInt64{Int64: value, Valid: true}
}

func nullableString(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}

func writeTransitionStatus(
	w http.ResponseWriter,
	changed int64,
	err error,
) bool {
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return false
	}
	if changed != 1 {
		w.WriteHeader(http.StatusConflict)
		return false
	}
	return true
}

func writeLifecycleStatus(w http.ResponseWriter, err error) bool {
	if errors.Is(err, repository.ErrRemoteLifecycleConflict) {
		w.WriteHeader(http.StatusConflict)
		return false
	}
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return false
	}
	return true
}

func writeJSON[T agentproto.HeartbeatResponse | agentproto.PollResponse](
	w http.ResponseWriter,
	response T,
) {
	payload, err := json.Marshal(response)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(payload)
}
