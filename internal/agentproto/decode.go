package agentproto

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

type wireProtocolEnvelope struct {
	Protocol json.RawMessage `json:"protocol"`
}

func DecodeRequest[T Request](body io.Reader) (T, error) {
	return decodeRequest[T](body, MaxRequestBytes, ErrRequestTooLarge)
}

func DecodeLogBatch(body io.Reader) (LogBatchRequest, error) {
	batch, err := decodeRequest[LogBatchRequest](
		body,
		MaxLogBatchBytes,
		ErrLogBatchTooLarge,
	)
	if err != nil {
		return LogBatchRequest{}, err
	}
	if len(batch.Events) > MaxLogEvents {
		return LogBatchRequest{}, protocolError(
			"events",
			ReasonLimit,
			ErrLogEventLimit,
		)
	}
	for _, event := range batch.Events {
		if len(event.Line) > MaxLogLineBytes {
			return LogBatchRequest{}, protocolError(
				"events.line",
				ReasonLimit,
				ErrLogLineTooLarge,
			)
		}
	}
	return batch, nil
}

func decodeRequest[T Request](
	body io.Reader,
	maxBytes int,
	limitError error,
) (T, error) {
	var request T
	payload, err := io.ReadAll(io.LimitReader(body, int64(maxBytes)+1))
	if err != nil {
		return request, protocolError("request", ReasonInvalid, ErrInvalidJSON)
	}
	if len(payload) > maxBytes {
		return request, protocolError("request", ReasonLimit, limitError)
	}
	if !utf8.Valid(payload) {
		return request, protocolError("request", ReasonInvalid, ErrInvalidJSON)
	}
	wireProtocol, err := decodeWireProtocol(payload)
	if err != nil {
		return request, err
	}

	decoder := jsonDecoder(payload)
	if err := decoder.Decode(&request); err != nil {
		return request, decodeError(err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return request, protocolError("request", ReasonInvalid, ErrInvalidJSON)
	}
	if request.protocolVersion() != wireProtocol {
		return request, protocolError(
			"protocol",
			ReasonInvalid,
			ErrUnsupportedProtocol,
		)
	}
	return request, nil
}

func decodeWireProtocol(payload []byte) (ProtocolVersion, error) {
	var envelope wireProtocolEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return "", protocolError("request", ReasonInvalid, ErrInvalidJSON)
	}
	if len(envelope.Protocol) == 0 {
		return "", protocolError(
			"protocol",
			ReasonInvalid,
			ErrUnsupportedProtocol,
		)
	}

	var protocol ProtocolVersion
	if err := protocol.UnmarshalJSON(envelope.Protocol); err != nil {
		return "", err
	}
	return protocol, nil
}

func jsonDecoder(payload []byte) *json.Decoder {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	return decoder
}

func decodeError(err error) error {
	if errors.Is(err, ErrUnsupportedProtocol) ||
		errors.Is(err, ErrInvalidResultState) ||
		errors.Is(err, ErrInvalidJSON) {
		return err
	}
	if strings.HasPrefix(err.Error(), "json: unknown field ") {
		return protocolError("request", ReasonUnknown, ErrUnknownField)
	}
	return protocolError("request", ReasonInvalid, ErrInvalidJSON)
}
