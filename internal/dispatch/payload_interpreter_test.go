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

func TestPayloadDecodeWithoutInterpreterMatchesBash(t *testing.T) {
	// Given: a v1-style payload whose step omits the interpreter field,
	// and the same payload with an explicit bash interpreter.
	legacy := `{"release":{"steps":[{"name":"s","script_body":"echo hi",` +
		`"sort_order":1,"timeout_seconds":5,"max_retries":0}]}}`
	modern := `{"release":{"steps":[{"name":"s","script_body":"echo hi",` +
		`"interpreter":"bash","sort_order":1,"timeout_seconds":5,` +
		`"max_retries":0}]}}`

	// When
	var decodedLegacy, decodedModern Payload
	if err := json.Unmarshal([]byte(legacy), &decodedLegacy); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(modern), &decodedModern); err != nil {
		t.Fatal(err)
	}

	// Then: the absent field decodes to the legacy empty sentinel, which
	// both the agent payload decoder and the runner's remote-step path map
	// to bash; after a marshal round-trip the absent and explicit-bash
	// payloads are byte-identical on the wire.
	if decodedLegacy.Release.Steps[0].Interpreter != "" {
		t.Fatalf(
			"legacy interpreter = %q, want empty",
			decodedLegacy.Release.Steps[0].Interpreter,
		)
	}
	legacyEncoded, err := json.Marshal(decodedLegacy)
	if err != nil {
		t.Fatal(err)
	}
	modernEncoded, err := json.Marshal(decodedModern)
	if err != nil {
		t.Fatal(err)
	}
	if string(legacyEncoded) != string(modernEncoded) {
		t.Fatalf(
			"absent interpreter encodes as %s, want %s",
			legacyEncoded,
			modernEncoded,
		)
	}
}
