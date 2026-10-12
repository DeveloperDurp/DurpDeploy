package agentserver

import (
	"net/http"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/dispatch"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/staging"

	agentartifact "github.com/DeveloperDurp/durpdeploy-agent/artifact"
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
	stagingSupported := r.Header.Get(
		staging.CapabilityHeader,
	) == staging.Capability
	artifactSupported := r.Header.Get(
		agentartifact.CapabilityHeader,
	) == agentartifact.Capability
	changed, err := s.repository.RecordAgentPollCapabilities(
		r.Context(),
		db.HeartbeatAgentParams{
			Now: nullableInt64(time.Now().Unix()),
			AgentVersion: nullableString(
				string(request.AgentVersion),
			),
			ID:                     string(agentID),
			CertificateFingerprint: nullableString(fingerprint),
		},
		repository.AgentPollCapabilities{
			PollRequest: request,
			Staging:     stagingSupported,
			Artifact:    artifactSupported,
		},
	)
	if !writeTransitionStatus(w, changed, err) {
		return
	}
	response, claimed, err := s.dispatcher.Poll(
		r.Context(),
		agentID,
		dispatch.PollRequest{
			PollRequest: request,
			Staging:     stagingSupported,
			Artifact:    artifactSupported,
		},
	)
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
