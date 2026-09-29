package api

import (
	"encoding/json"
	"net/http"

	"durpdeploy/internal/containerenv"
	"durpdeploy/internal/handler"
)

// validateContainerConfig validates the container mode fields shared
// by step and step-template requests. Local steps always run in a
// server container, so the image is mandatory; agent steps run on the
// host and cannot carry an image. Returns the trimmed image, the
// validated variable-name list (nil when absent), and false when the
// error response was already written.
func validateContainerConfig(
	w http.ResponseWriter,
	target, containerImage string,
	variableNames []string,
) (string, []string, bool) {
	image := containerImage
	if target == "agent" {
		if image != "" {
			RespondError(
				w,
				http.StatusBadRequest,
				"Agent steps cannot use a container image",
			)
			return "", nil, false
		}
	} else if image == "" {
		RespondError(
			w,
			http.StatusBadRequest,
			"Container image is required for local steps",
		)
		return "", nil, false
	} else if !handler.ValidContainerImage(image) {
		RespondError(w, http.StatusBadRequest, "Invalid container image")
		return "", nil, false
	}
	names, ok := validateVariableNames(w, variableNames, target == "local")
	if !ok {
		return "", nil, false
	}
	return image, names, true
}

// validateVariableNames checks that every entry is an identifier and
// that no name appears twice, since the allowlist is stored as a
// JSON array.
func validateVariableNames(
	w http.ResponseWriter,
	variableNames []string,
	local bool,
) ([]string, bool) {
	if len(variableNames) == 0 {
		return nil, true
	}
	seen := make(map[string]struct{}, len(variableNames))
	for _, name := range variableNames {
		switch containerenv.ValidateName(name, local) {
		case containerenv.ErrIdentifier:
			RespondError(
				w,
				http.StatusBadRequest,
				"Variable names must be identifiers: letters, digits, "+
					"underscores, and cannot start with a digit",
			)
			return nil, false
		case containerenv.ErrReserved:
			RespondError(
				w,
				http.StatusBadRequest,
				containerenv.ErrReserved.Error(),
			)
			return nil, false
		}
		if _, dup := seen[name]; dup {
			RespondError(
				w,
				http.StatusBadRequest,
				"Variable names contain duplicates",
			)
			return nil, false
		}
		seen[name] = struct{}{}
	}
	return variableNames, true
}

// marshalVariableNames encodes the allowlist as the JSON array string
// the database column stores. An empty allowlist stays an empty string.
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
