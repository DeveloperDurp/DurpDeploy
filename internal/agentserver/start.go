package agentserver

import (
	"net/http"

	"durpdeploy/internal/repository"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

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
	claim := repository.RemoteLifecycleClaim{
		DeploymentID:   deploymentID,
		AgentID:        string(agentID),
		ClaimTokenHash: claimTokenHash(request.ClaimToken),
	}
	scrubber, err := s.remoteLogScrubber(r.Context(), deploymentID)
	if !writeLifecycleStatus(w, err) {
		return
	}
	inserted, handled, err := s.repository.StartRemoteStep(
		r.Context(), claim, scrubber,
	)
	if err == nil && !handled {
		err = s.repository.StartRemoteDeployment(r.Context(), claim)
	}
	if writeLifecycleStatus(w, err) {
		s.broadcastLogs(deploymentID, inserted)
		w.WriteHeader(http.StatusNoContent)
	}
}
