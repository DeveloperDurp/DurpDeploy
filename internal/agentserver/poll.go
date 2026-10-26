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
	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
)

func (s *Server) Poll(w http.ResponseWriter, r *http.Request) {
	request, ok := decodeRequest[agentproto.PollRequest](w, r)
	if !ok {
		return
	}
	policy := s.repository.ResourceLimits().AgentPolicy()
	values := r.Header.Values(resourcepolicy.CapabilityHeader)
	advertisement := ""
	if len(values) > 1 || (len(values) == 1 && values[0] == "") {
		w.WriteHeader(http.StatusConflict)
		return
	}
	if len(values) == 1 {
		advertisement = values[0]
	}
	if err := policy.CheckCompatibility(advertisement); err != nil {
		writePollErrorStatus(w, err)
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
			PollRequest:    request,
			Staging:        stagingSupported,
			Artifact:       artifactSupported,
			ResourcePolicy: advertisement,
		},
	)
	if !writeTransitionStatus(w, changed, err) {
		return
	}
	response, claimed, err := s.dispatcher.Poll(
		r.Context(),
		agentID,
		dispatch.PollRequest{
			PollRequest:    request,
			Staging:        stagingSupported,
			Artifact:       artifactSupported,
			ResourcePolicy: advertisement,
		},
	)
	if err != nil {
		if r.Context().Err() == nil {
			writePollErrorStatus(w, err)
		}
		return
	}
	acknowledgement, err := policy.Advertisement()
	if err != nil {
		writePollErrorStatus(w, err)
		return
	}
	w.Header().Set(resourcepolicy.CapabilityHeader, acknowledgement)
	if !claimed {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, response)
}
