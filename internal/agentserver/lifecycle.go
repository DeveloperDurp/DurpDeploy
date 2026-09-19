package agentserver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"

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
	scrubber, err := s.remoteLogScrubber(r.Context(), deploymentID)
	if !writeLifecycleStatus(w, err) {
		return
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
		r.Context(), claim, events, scrubber,
	)
	if err == nil && !handled {
		inserted, err = s.repository.AppendRemoteDeploymentLogs(
			r.Context(), claim, events, scrubber,
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
	scrubber, err := s.remoteLogScrubber(r.Context(), deploymentID)
	if !writeLifecycleStatus(w, err) {
		return
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
	var inserted []db.DeploymentLog
	if err == nil && handled {
		inserted, _, err = s.repository.FlushRemoteStepLogs(
			r.Context(), claim, scrubber,
		)
	} else if err == nil {
		inserted, err = s.repository.FlushRemoteDeploymentLogs(
			r.Context(), claim, scrubber,
		)
	}
	if !writeLifecycleStatus(w, err) {
		return
	}
	s.broadcastLogs(deploymentID, inserted)
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
	scrubber, err := s.remoteLogScrubber(r.Context(), deploymentID)
	if !writeLifecycleStatus(w, err) {
		return
	}
	_, handled, err := s.repository.AcknowledgeRemoteStepCancellation(
		r.Context(), claim,
	)
	if err == nil && !handled {
		_, err = s.repository.AcknowledgeRemoteCancellation(
			r.Context(), claim,
		)
	}
	var inserted []db.DeploymentLog
	if err == nil && handled {
		inserted, _, err = s.repository.FlushRemoteStepLogs(
			r.Context(), claim, scrubber,
		)
	} else if err == nil {
		inserted, err = s.repository.FlushRemoteDeploymentLogs(
			r.Context(), claim, scrubber,
		)
	}
	if !writeLifecycleStatus(w, err) {
		return
	}
	s.broadcastLogs(deploymentID, inserted)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) remoteLogScrubber(
	ctx context.Context,
	deploymentID int64,
) (*runner.Scrubber, error) {
	deployment, err := s.repository.Queries.GetDeployment(ctx, deploymentID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, repository.ErrRemoteLifecycleConflict
		}
		return nil, err
	}
	variables, err := s.repository.ListReleaseVariablesByRelease(
		ctx, deployment.ReleaseID,
	)
	if err != nil {
		return nil, err
	}
	resolved, err := runner.ResolveReleaseVariables(
		variables, deployment.EnvironmentID,
	)
	if err != nil {
		return nil, err
	}
	secrets := make([]string, 0, len(resolved))
	for _, variable := range resolved {
		if variable.Secret && variable.Value != "" {
			secrets = append(secrets, variable.Value)
		}
	}
	return runner.NewScrubber(secrets), nil
}

func (s *Server) broadcastLogs(
	deploymentID int64,
	logs []db.DeploymentLog,
) {
	if s.broker == nil {
		return
	}
	for _, log := range logs {
		s.broker.Broadcast(deploymentID, log.Line)
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
