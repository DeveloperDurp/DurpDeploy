package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"durpdeploy/internal/agentexecution"
	"durpdeploy/internal/artifact"
	"durpdeploy/internal/interpreter"
	"durpdeploy/internal/stepdefinition"
)

type runbookFormStep stepdefinition.Definition

func (step runbookFormStep) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		stepdefinition.Definition
		VariableNames []string `json:"variable_names,omitempty"`
	}{stepdefinition.Definition(step), step.VariableNames})
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
	if len(names) == 0 {
		return "", errors.New("add at least one step")
	}
	if !completeRunbookStepFields(r.Form, len(names)) {
		return "", errors.New("incomplete step fields")
	}
	available, err := h.repo.Queries.ListAvailableAgentLabels(r.Context())
	if err != nil {
		return "", err
	}
	steps := make([]runbookFormStep, len(names))
	for i := range names {
		step, err := decodeRunbookFormStep(r.Form, i)
		if err != nil {
			return "", err
		}
		if err := h.validateRunbookFormStep(r.Form, i, &step, available); err != nil {
			return "", err
		}
		steps[i] = runbookFormStep(step)
	}
	encoded, err := json.Marshal(steps)
	return string(encoded), err
}

func completeRunbookStepFields(form url.Values, count int) bool {
	required := []string{"step_script", "step_interpreter", "step_timeout",
		"step_retries", "step_target", "step_selectors"}
	for _, field := range required {
		if len(form[field]) != count {
			return false
		}
	}
	// Optional fields may be absent or supply one value for each step.
	optional := []string{"step_kind", "step_ansible", "step_agent_mode",
		"step_image", "step_network", "step_variable_names"}
	for _, field := range optional {
		if size := len(form[field]); size != 0 && size != count {
			return false
		}
	}
	return true
}

func optionalRunbookStepValue(values []string, i int, fallback string) string {
	if len(values) == 0 {
		return fallback
	}
	return values[i]
}

func decodeRunbookFormStep(
	form url.Values,
	i int,
) (stepdefinition.Definition, error) {
	name := strings.TrimSpace(form["step_name"][i])
	kind := optionalRunbookStepValue(form["step_kind"], i, "")
	ansible, err := stepdefinition.DecodeAnsible(
		optionalRunbookStepValue(form["step_ansible"], i, ""),
	)
	if err != nil {
		return stepdefinition.Definition{}, err
	}
	script := form["step_script"][i]
	if name == "" ||
		kind != stepdefinition.AnsibleKind && strings.TrimSpace(script) == "" {
		return stepdefinition.Definition{}, errors.New(
			"each step needs a name and script",
		)
	}
	selected := ""
	if kind != stepdefinition.AnsibleKind {
		selected, err = interpreter.Validate(form["step_interpreter"][i])
		if err != nil {
			return stepdefinition.Definition{}, err
		}
	}
	timeout, err := formNumber(form["step_timeout"][i])
	if err != nil || timeout < 0 {
		return stepdefinition.Definition{}, errors.New("invalid step timeout")
	}
	retries, err := formNumber(form["step_retries"][i])
	if err != nil || retries < 0 {
		return stepdefinition.Definition{}, errors.New("invalid step retries")
	}
	return stepdefinition.Definition{
		Kind: kind, Ansible: ansible,
		Name: name, ScriptBody: script, Interpreter: selected,
		SortOrder: int64(i), TimeoutSeconds: timeout, MaxRetries: retries,
	}, nil
}

func splitRunbookStepList(raw string) []string {
	var values []string
	for value := range strings.SplitSeq(raw, ",") {
		if value = strings.TrimSpace(value); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func (h *RunbookHandler) validateRunbookFormStep(
	form url.Values,
	i int,
	step *stepdefinition.Definition,
	available []string,
) error {
	if err := agentexecution.ValidateLimitsWithPolicy(
		step.TimeoutSeconds, step.MaxRetries, h.repo.ResourceLimits().Execution,
	); err != nil {
		return err
	}
	target, selectors, err := ValidateStepPlacement(
		form["step_target"][i],
		splitRunbookStepList(form["step_selectors"][i]),
		available,
	)
	if err != nil {
		return err
	}
	step.ExecutionTarget = target
	step.AgentSelectors = selectors
	step.ContainerImage = optionalRunbookStepValue(form["step_image"], i, "")
	step.VariableNames = splitRunbookStepList(
		optionalRunbookStepValue(form["step_variable_names"], i, ""),
	)
	step.AgentExecutionMode = optionalRunbookStepValue(
		form["step_agent_mode"],
		i,
		"host",
	)
	if message := ValidateStepExecutionConfig(agentexecution.Config{
		Target: target, Mode: step.AgentExecutionMode, Image: step.ContainerImage,
		VariableNames: step.VariableNames,
	}); message != "" {
		return errors.New(strings.ToLower(message[:1]) + message[1:])
	}
	step.NetworkMode = optionalRunbookStepValue(form["step_network"], i, "")
	if err := artifact.ValidateGateConfig(target, step.NetworkMode, "", "", ""); err != nil {
		return err
	}
	return step.ValidateWrite()
}
