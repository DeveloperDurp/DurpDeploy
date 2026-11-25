package agentserver

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"

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
	acknowledgement, ok := legacyPolicyAcknowledgement(
		r.Header.Values(resourcepolicy.CapabilityHeader),
	)
	if !ok {
		w.WriteHeader(http.StatusConflict)
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
	if acknowledgement != "" {
		w.Header().Set(resourcepolicy.CapabilityHeader, acknowledgement)
	}
	if !claimed {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, response)
}

// Version 1 clients expect their own digest on a successful poll.
func legacyPolicyAcknowledgement(values []string) (string, bool) {
	if len(values) == 0 {
		return "", true
	}
	if len(values) != 1 {
		return "", false
	}
	digest, ok := strings.CutPrefix(
		values[0], resourcepolicy.Capability+";sha256=",
	)
	if !ok || len(digest) != sha256.Size*2 ||
		digest != strings.ToLower(digest) {
		return "", false
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return "", false
	}
	return values[0], true
}
