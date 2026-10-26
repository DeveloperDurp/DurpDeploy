package api

import (
	"encoding/json"
)

func marshalVariableNames(variableNames []string) string {
	if len(variableNames) == 0 {
		return ""
	}
	encoded, err := json.Marshal(variableNames)
	if err != nil {
		return ""
	}
	return string(encoded)
}
