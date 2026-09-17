package agentserver

import (
	"fmt"
	"net/http"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
	"durpdeploy/internal/repository"

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
	changed, err := s.repository.HeartbeatAgent(
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
	handled, err := s.repository.StartRemoteStep(r.Context(), claim)
	if err == nil && !handled {
		err = s.repository.StartRemoteDeployment(r.Context(), claim)
	}
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
	claim := repository.RemoteLifecycleClaim{
		DeploymentID:   deploymentID,
		AgentID:        string(agentID),
		ClaimTokenHash: claimTokenHash(request.ClaimToken),
	}
	result, handled, err := s.repository.HeartbeatRemoteStep(r.Context(), claim)
	if err == nil && !handled {
		result, err = s.repository.HeartbeatRemoteDeployment(r.Context(), claim)
	}
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
	claim := repository.RemoteLifecycleClaim{
		DeploymentID:   deploymentID,
		AgentID:        string(agentID),
		ClaimTokenHash: claimTokenHash(request.ClaimToken),
	}
	events := make([]repository.RemoteLogEvent, len(request.Events))
	for _, event := range request.Events {
		if event.Sequence < 0 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
	}
	for index, event := range request.Events {
		events[index] = repository.RemoteLogEvent{
			Sequence: int64(event.Sequence),
			Line:     event.Line,
		}
	}
	inserted, handled, err := s.repository.AppendRemoteStepLogs(
		r.Context(), claim, events,
	)
	if err == nil && !handled {
		inserted, err = s.repository.AppendRemoteDeploymentLogs(
			r.Context(), claim, events,
		)
	}
	if !writeLifecycleStatus(w, err) {
		return
	}
	if s.broker != nil {
		for _, log := range inserted {
			s.broker.Broadcast(deploymentID, log.Line)
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
	claim := repository.RemoteLifecycleClaim{
		DeploymentID:   deploymentID,
		AgentID:        string(agentID),
		ClaimTokenHash: claimTokenHash(request.ClaimToken),
	}
	handled, err := s.repository.FinishRemoteStep(
		r.Context(), claim, string(request.State),
	)
	result := repository.RemoteTerminalResult{}
	if err == nil && !handled {
		result, err = s.repository.FinishRemoteDeploymentLifecycle(
			r.Context(), claim, string(request.State),
		)
	}
	if !writeLifecycleStatus(w, err) {
		return
	}
	if result.Changed {
		s.publishRemoteResult(r, result)
	}
	w.WriteHeader(http.StatusNoContent)
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
	claim := repository.RemoteLifecycleClaim{
		DeploymentID:   deploymentID,
		AgentID:        string(agentID),
		ClaimTokenHash: claimTokenHash(request.ClaimToken),
	}
	_, handled, err := s.repository.AcknowledgeRemoteStepCancellation(
		r.Context(), claim,
	)
	if err == nil && !handled {
		_, err = s.repository.AcknowledgeRemoteCancellation(
			r.Context(), claim,
		)
	}
	if writeLifecycleStatus(w, err) {
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) publishRemoteResult(
	r *http.Request,
	result repository.RemoteTerminalResult,
) {
	if s.eventBus == nil {
		return
	}
	typ := events.DeploymentSucceeded
	message := fmt.Sprintf(
		"Deployment #%d succeeded on %s",
		result.DeploymentID,
		result.EnvironmentName,
	)
	if result.State == "failed" {
		typ = events.DeploymentFailed
		message = fmt.Sprintf(
			"Deployment #%d failed on %s",
			result.DeploymentID,
			result.EnvironmentName,
		)
	}
	s.eventBus.Publish(r.Context(), events.Event{
		Type:          typ,
		DeploymentID:  result.DeploymentID,
		ProjectID:     result.ProjectID,
		EnvironmentID: result.EnvironmentID,
		Message:       message,
	})
}
