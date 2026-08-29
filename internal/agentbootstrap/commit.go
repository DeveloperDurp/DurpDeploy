package agentbootstrap

import (
	"errors"
	"net/http"

	"durpdeploy/internal/agentproto"
	"durpdeploy/internal/agentstate"
	"durpdeploy/internal/agenttls"
)

func (listener *Listener) serverInit(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if request.Method != http.MethodPost {
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	pairRequest, err := agentproto.DecodeRequest[agentproto.PairRequest](
		request.Body,
	)
	if err != nil || pairRequest.AgentID == "" || request.TLS == nil ||
		len(request.TLS.PeerCertificates) != 1 {
		writer.WriteHeader(http.StatusUnauthorized)
		return
	}
	serverFingerprint := agenttls.FingerprintOf(
		request.TLS.PeerCertificates[0].Raw,
	)
	serverPin, err := agentproto.ParseSHA256Pin(serverFingerprint.String())
	if err != nil || serverPin != pairRequest.ServerPin {
		writer.WriteHeader(http.StatusUnauthorized)
		return
	}
	_, err = listener.session.PairAndCommit(
		listener.now(),
		agentproto.PairingConfirmation{
			Code: pairRequest.PairingCode, ObservedAgent: pairRequest.AgentPin,
			ServerPin: serverPin, PullEndpoint: pairRequest.PullEndpoint,
		},
		func() error {
			state, stateErr := agentstate.New(
				pairRequest.PullEndpoint.String(),
				[]agenttls.Fingerprint{serverFingerprint},
				pairRequest.AgentID,
			)
			if stateErr != nil {
				return stateErr
			}
			state.AgentVersion = string(listener.agentVersion)
			return listener.store.Save(state)
		},
	)
	if err != nil {
		switch {
		case errors.Is(err, agentproto.ErrPairingCodeUsed):
			writer.WriteHeader(http.StatusGone)
		default:
			writer.WriteHeader(http.StatusUnauthorized)
		}
		return
	}
	if listener.afterPairCommit != nil && listener.afterPairCommit(writer) {
		return
	}
	writer.WriteHeader(http.StatusNoContent)
	if pairRequest.CompletionAck {
		listener.pairedOnce.Do(func() { close(listener.paired) })
	}
}
