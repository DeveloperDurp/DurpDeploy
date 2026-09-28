package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"durpdeploy/internal/interpreter"
)

type runbookFormStep struct {
	Name            string   `json:"name"`
	ScriptBody      string   `json:"script_body"`
	Interpreter     string   `json:"interpreter"`
	SortOrder       int64    `json:"sort_order"`
	TimeoutSeconds  int64    `json:"timeout_seconds"`
	MaxRetries      int64    `json:"max_retries"`
	ExecutionTarget string   `json:"execution_target"`
	AgentSelectors  []string `json:"agent_selectors"`
	ContainerImage  string   `json:"container_image"`
	VariableNames   []string `json:"variable_names,omitempty"`
}

// variableNamePattern restricts variable_names entries to Go-style
// identifiers so they can be exported as environment variables inside
// the step container.
var variableNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidateRunbookStepContainer validates the server-container fields of
// a runbook step. Local steps run inside a mandatory server container,
// so a container image is required; agent steps run on the remote host
// and must not carry an image. Variable names become the container
// environment allowlist and must be unique identifiers.
func ValidateRunbookStepContainer(
	target, containerImage string,
	variableNames []string,
) (string, []string, error) {
	image := strings.TrimSpace(containerImage)
	switch {
	case target == "local" && image == "":
		return "", nil, errors.New(
			"container image is required for local steps",
		)
	case image != "" && target != "local":
		return "", nil, errors.New(
			"container image is only valid for local steps",
		)
	}
	seen := make(map[string]struct{}, len(variableNames))
	for _, name := range variableNames {
		if !variableNamePattern.MatchString(name) {
			return "", nil, errors.New(
				"variable names must be identifiers: letters, " +
					"digits, underscores, and cannot start with a digit",
			)
		}
		if _, dup := seen[name]; dup {
			return "", nil, errors.New(
				"variable names contain duplicates",
			)
		}
		seen[name] = struct{}{}
	}
	return image, variableNames, nil
}

func formNumber(value string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	return strconv.ParseInt(value, 10, 64)
}

func (h *RunbookHandler) formSteps(r *http.Request) (string, error) {
	names := r.Form["step_name"]
	fields := []string{"step_script", "step_interpreter", "step_timeout",
		"step_retries", "step_target", "step_selectors"}
	if len(names) == 0 {
		return "", errors.New("add at least one step")
	}
	for _, field := range fields {
		if len(r.Form[field]) != len(names) {
			return "", errors.New("incomplete step fields")
		}
	}
	images := r.Form["step_image"]
	variableLists := r.Form["step_variable_names"]
	// Absent arrays mean empty values for every step; a partially
	// submitted array is a malformed form.
	if len(images) != 0 && len(images) != len(names) ||
		len(variableLists) != 0 && len(variableLists) != len(names) {
		return "", errors.New("incomplete step fields")
	}
	available, err := h.repo.Queries.ListAvailableAgentLabels(r.Context())
	if err != nil {
		return "", err
	}
	steps := make([]runbookFormStep, len(names))
	for i, name := range names {
		name = strings.TrimSpace(name)
		script := r.Form["step_script"][i]
		if name == "" || strings.TrimSpace(script) == "" {
			return "", errors.New("each step needs a name and script")
		}
		selected, err := interpreter.Validate(r.Form["step_interpreter"][i])
		if err != nil {
			return "", err
		}
		timeout, err := formNumber(r.Form["step_timeout"][i])
		if err != nil || timeout < 0 {
			return "", errors.New("invalid step timeout")
		}
		retries, err := formNumber(r.Form["step_retries"][i])
		if err != nil || retries < 0 {
			return "", errors.New("invalid step retries")
		}
		var selectors []string
		for _, label := range strings.Split(r.Form["step_selectors"][i], ",") {
			if label = strings.TrimSpace(label); label != "" {
				selectors = append(selectors, label)
			}
		}
		target, selectors, err := ValidateStepPlacement(
			r.Form["step_target"][i], selectors, available,
		)
		if err != nil {
			return "", err
		}
		image := ""
		if len(images) == len(names) {
			image = images[i]
		}
		var variableNames []string
		if len(variableLists) == len(names) {
			for _, raw := range strings.Split(variableLists[i], ",") {
				if raw = strings.TrimSpace(raw); raw != "" {
					variableNames = append(variableNames, raw)
				}
			}
		}
		image, variableNames, err = ValidateRunbookStepContainer(
			target, image, variableNames,
		)
		if err != nil {
			return "", err
		}
		steps[i] = runbookFormStep{
			Name: name, ScriptBody: script, Interpreter: selected,
			SortOrder: int64(i), TimeoutSeconds: timeout,
			MaxRetries: retries, ExecutionTarget: target,
			AgentSelectors: selectors, ContainerImage: image,
			VariableNames: variableNames,
		}
	}
	encoded, err := json.Marshal(steps)
	return string(encoded), err
}
