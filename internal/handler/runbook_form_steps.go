package handler

import (
	"encoding/json"
	"errors"
	"net/http"
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
		steps[i] = runbookFormStep{
			Name: name, ScriptBody: script, Interpreter: selected,
			SortOrder: int64(i), TimeoutSeconds: timeout,
			MaxRetries: retries, ExecutionTarget: target,
			AgentSelectors: selectors,
		}
	}
	encoded, err := json.Marshal(steps)
	return string(encoded), err
}
