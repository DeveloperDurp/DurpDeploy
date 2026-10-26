package handler

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunbookEditorStepsPreservesRetries(t *testing.T) {
	for _, retries := range []string{"0", "10", "9007199254740993", "9223372036854775806"} {
		encoded, err := runbookEditorSteps(
			`[{"max_retries":` + retries + `,"script_body":"echo max_retries: 123","variable_names":["API_URL"]}]`,
		)
		require.NoError(t, err)
		var steps []struct {
			MaxRetries    string   `json:"max_retries"`
			ScriptBody    string   `json:"script_body"`
			VariableNames []string `json:"variable_names"`
		}
		require.NoError(t, json.Unmarshal([]byte(encoded), &steps))
		require.Len(t, steps, 1)
		require.Equal(t, retries, steps[0].MaxRetries)
		require.Equal(t, "echo max_retries: 123", steps[0].ScriptBody)
		require.Equal(t, []string{"API_URL"}, steps[0].VariableNames)
	}
	for _, raw := range []string{"[", "[null]", `[{"max_retries":"3"}]`, `[{"max_retries":9223372036854775808}]`} {
		_, err := runbookEditorSteps(raw)
		require.Error(t, err)
	}
}
