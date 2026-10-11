package agentserver

import (
	"net/http"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/staging"

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
	changed, err := s.repository.RecordAgentPollWithStaging(
		r.Context(),
		db.HeartbeatAgentParams{
			Now: nullableInt64(time.Now().Unix()),
			AgentVersion: nullableString(
				string(request.AgentVersion),
			),
			ID:                     string(agentID),
			CertificateFingerprint: nullableString(fingerprint),
		},
		request,
		r.Header.Get(staging.CapabilityHeader) == staging.Capability,
	)
	if !writeTransitionStatus(w, changed, err) {
		return
	}
	response, claimed, err := s.dispatcher.Poll(r.Context(), agentID, request)
	if err != nil {
		if r.Context().Err() == nil {
			writePollErrorStatus(w, err)
		}
		return
	}
	if !claimed {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, response)
}
