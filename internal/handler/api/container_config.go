package api

import (
	"encoding/json"
	"net/http"

	"durpdeploy/internal/agentexecution"
	"durpdeploy/internal/handler"
)

func validateContainerConfig(
	w http.ResponseWriter,
	config agentexecution.Config,
) (string, []string, bool) {
	if message := handler.ValidateStepExecutionConfig(config); message != "" {
		RespondError(w, http.StatusBadRequest, message)
		return "", nil, false
	}
	return config.Image, config.VariableNames, true
}

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
