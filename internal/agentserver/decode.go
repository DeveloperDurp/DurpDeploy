package agentserver

import (
	"errors"
	"net/http"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func decodeRequest[T agentproto.Request](
	w http.ResponseWriter,
	r *http.Request,
) (T, bool) {
	request, err := agentproto.DecodeRequest[T](r.Body)
	if err != nil {
		w.WriteHeader(protocolErrorStatus(err))
		return request, false
	}
	return request, true
}

func decodeLogBatch(
	w http.ResponseWriter,
	r *http.Request,
) (agentproto.LogBatchRequest, bool) {
	request, err := agentproto.DecodeLogBatch(r.Body)
	if err != nil {
		w.WriteHeader(protocolErrorStatus(err))
		return request, false
	}
	return request, true
}

func protocolErrorStatus(err error) int {
	if errors.Is(err, agentproto.ErrRequestTooLarge) ||
		errors.Is(err, agentproto.ErrLogBatchTooLarge) ||
		errors.Is(err, agentproto.ErrLogEventLimit) ||
		errors.Is(err, agentproto.ErrLogLineTooLarge) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}
