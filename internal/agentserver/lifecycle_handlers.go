package agentserver

import (
	"encoding/json"
	"net/http"
	"strconv"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func (server *Server) start(writer http.ResponseWriter, request *http.Request) {
	decoded, ok := decodeLifecycleRequest[agentproto.StartRequest](
		writer,
		request,
	)
	if !ok {
		return
	}
	deploymentID, ok := deploymentID(writer, request)
	if !ok {
		return
	}
	agent := agentFromContext(request.Context())
	if err := server.startDeployment(
		request.Context(),
		deploymentID,
		agent.ID,
		decoded.ClaimToken,
	); err != nil {
		server.recordLifecycleConflict(
			request.Context(),
			deploymentID,
			agent.ID,
			decoded.ClaimToken,
			err,
		)
		server.writeLifecycleError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (server *Server) heartbeat(
	writer http.ResponseWriter,
	request *http.Request,
) {
	decoded, ok := decodeLifecycleRequest[agentproto.HeartbeatRequest](
		writer,
		request,
	)
	if !ok {
		return
	}
	deploymentID, ok := deploymentID(writer, request)
	if !ok {
		return
	}
	agent := agentFromContext(request.Context())
	cancelRequested, err := server.heartbeatDeployment(
		request.Context(),
		deploymentID,
		agent.ID,
		decoded.ClaimToken,
	)
	if err != nil {
		server.recordLifecycleConflict(
			request.Context(),
			deploymentID,
			agent.ID,
			decoded.ClaimToken,
			err,
		)
		server.writeLifecycleError(writer, err)
		return
	}
	pins := []agentproto.CertificateFingerprint{
		agentproto.CertificateFingerprint(server.identity.Fingerprint.String()),
	}
	if server.pendingServerPin != nil {
		pins = append(
			pins,
			agentproto.CertificateFingerprint(server.pendingServerPin.String()),
		)
	}
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(agentproto.HeartbeatResponse{
		CancelRequested: cancelRequested,
		ServerPins:      pins,
	}); err != nil {
		return
	}
}

func (server *Server) logs(writer http.ResponseWriter, request *http.Request) {
	decoded, err := agentproto.DecodeLogBatch(request.Body)
	if err != nil {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	deploymentID, ok := deploymentID(writer, request)
	if !ok {
		return
	}
	agent := agentFromContext(request.Context())
	logIDs, err := server.writeLogs(
		request.Context(),
		deploymentID,
		agent.ID,
		decoded,
	)
	if err != nil {
		server.recordLifecycleConflict(
			request.Context(),
			deploymentID,
			agent.ID,
			decoded.ClaimToken,
			err,
		)
		server.writeLifecycleError(writer, err)
		return
	}
	if server.broker != nil {
		for _, logID := range logIDs {
			server.broker.Broadcast(deploymentID, logID)
		}
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (server *Server) result(
	writer http.ResponseWriter,
	request *http.Request,
) {
	decoded, ok := decodeLifecycleRequest[agentproto.ResultRequest](
		writer,
		request,
	)
	if !ok {
		return
	}
	deploymentID, ok := deploymentID(writer, request)
	if !ok {
		return
	}
	agent := agentFromContext(request.Context())
	if err := server.completeDeployment(
		request.Context(),
		deploymentID,
		agent.ID,
		decoded,
	); err != nil {
		if !server.recordLateResult(
			request.Context(),
			deploymentID,
			agent.ID,
			decoded.ClaimToken,
		) {
			server.recordLifecycleConflict(
				request.Context(),
				deploymentID,
				agent.ID,
				decoded.ClaimToken,
				err,
			)
		}
		server.writeLifecycleError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (server *Server) cancelled(
	writer http.ResponseWriter,
	request *http.Request,
) {
	decoded, ok := decodeLifecycleRequest[agentproto.CancelledRequest](
		writer,
		request,
	)
	if !ok {
		return
	}
	deploymentID, ok := deploymentID(writer, request)
	if !ok {
		return
	}
	agent := agentFromContext(request.Context())
	if err := server.cancelDeployment(
		request.Context(),
		deploymentID,
		agent.ID,
		decoded.ClaimToken,
	); err != nil {
		server.recordLifecycleConflict(
			request.Context(),
			deploymentID,
			agent.ID,
			decoded.ClaimToken,
			err,
		)
		server.writeLifecycleError(writer, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func decodeLifecycleRequest[T agentproto.Request](
	writer http.ResponseWriter,
	request *http.Request,
) (T, bool) {
	decoded, err := agentproto.DecodeRequest[T](request.Body)
	if err != nil {
		writer.WriteHeader(http.StatusBadRequest)
		return decoded, false
	}
	return decoded, true
}

func deploymentID(
	writer http.ResponseWriter,
	request *http.Request,
) (int64, bool) {
	id, err := strconv.ParseInt(request.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writer.WriteHeader(http.StatusBadRequest)
		return 0, false
	}
	return id, true
}
