package agentproto

import (
	"errors"
	"strings"
	"testing"
)

type protocolUnawareRequest struct{}

func (protocolUnawareRequest) agentRequest() {}

func (protocolUnawareRequest) protocolVersion() ProtocolVersion {
	return AgentV1
}

func TestDecodeRequest_rejects_missing_wire_protocol_despite_dto_claim(
	t *testing.T,
) {
	tests := []struct {
		name string
		body string
	}{
		{name: "omitted", body: `{}`},
		{name: "null", body: `{"protocol":null}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			body := strings.NewReader(test.body)

			// When
			_, err := DecodeRequest[protocolUnawareRequest](body)

			// Then
			if !errors.Is(err, ErrUnsupportedProtocol) {
				t.Fatalf(
					"DecodeRequest() error = %v, want %v",
					err,
					ErrUnsupportedProtocol,
				)
			}
		})
	}
}
