package dispatch

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/DeveloperDurp/durpdeploy-agent/executor"
)

func TestPayloadMarshalOmitsBashInterpreterForLegacyAgents(t *testing.T) {
	// Given
	payload := Payload{
		Release: ReleaseSnapshot{
			Steps: []executor.Step{{
				Interpreter: executor.InterpreterBash,
			}},
		},
	}

	// When
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	// Then
	if strings.Contains(string(encoded), `"interpreter"`) {
		t.Fatalf("legacy Bash payload included interpreter: %s", encoded)
	}
}
