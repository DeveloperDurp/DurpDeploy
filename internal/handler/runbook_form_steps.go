package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"durpdeploy/internal/agentexecution"
	"durpdeploy/internal/artifact"
	"durpdeploy/internal/interpreter"
)

type runbookFormStep struct {
	Name                 string   `json:"name"`
	ScriptBody           string   `json:"script_body"`
	Interpreter          string   `json:"interpreter"`
	SortOrder            int64    `json:"sort_order"`
	TimeoutSeconds       int64    `json:"timeout_seconds"`
	MaxRetries           int64    `json:"max_retries"`
	ExecutionTarget      string   `json:"execution_target"`
	AgentExecutionMode   string   `json:"agent_execution_mode"`
	AgentSelectors       []string `json:"agent_selectors"`
	ContainerImage       string   `json:"container_image"`
	NetworkMode          string   `json:"network_mode"`
	ApprovalArtifactPath string   `json:"approval_artifact_path"`
	ApprovalReviewPath   string   `json:"approval_review_path"`
	ApprovalReviewFormat string   `json:"approval_review_format"`
	VariableNames        []string `json:"variable_names,omitempty"`
}

// ValidContainerImage mirrors the runner's image argument restrictions.
func ValidContainerImage(image string) bool {
	return image != "" && !strings.HasPrefix(image, "-") &&
		!strings.ContainsAny(image, " \t\r\n\x00")
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
	modes := r.Form["step_agent_mode"]
	networks := r.Form["step_network"]
	variableLists := r.Form["step_variable_names"]
	// Absent arrays mean empty values for every step; a partially
	// submitted array is a malformed form.
	if len(modes) != 0 && len(modes) != len(names) ||
		len(images) != 0 && len(images) != len(names) ||
		len(networks) != 0 && len(networks) != len(names) ||
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
		mode := "host"
		if len(modes) == len(names) {
			mode = modes[i]
		}
		if message := ValidateStepExecutionConfig(agentexecution.Config{
			Target: target, Mode: mode, Image: image, VariableNames: variableNames,
		}); message != "" {
			return "", errors.New(strings.ToLower(message[:1]) + message[1:])
		}
		network := ""
		if len(networks) == len(names) {
			network = networks[i]
		}
		if err := artifact.ValidateGateConfig(
			target,
			network,
			"",
			"",
			"",
		); err != nil {
			return "", err
		}
		steps[i] = runbookFormStep{
			Name: name, ScriptBody: script, Interpreter: selected,
			SortOrder: int64(i), TimeoutSeconds: timeout,
			MaxRetries: retries, ExecutionTarget: target,
			AgentExecutionMode: mode,
			AgentSelectors:     selectors, ContainerImage: image,
			NetworkMode:   network,
			VariableNames: variableNames,
		}
	}
	encoded, err := json.Marshal(steps)
	return string(encoded), err
}
