package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"durpdeploy/internal/audit"
	"durpdeploy/internal/auth"
	"durpdeploy/internal/interpreter"
	"durpdeploy/internal/stepdefinition"
	"durpdeploy/views/components"
)

func formAnsible(r *http.Request) (*stepdefinition.Ansible, error) {
	if r.FormValue("kind") != stepdefinition.AnsibleKind {
		return nil, nil
	}
	a := &stepdefinition.Ansible{
		Playbook: r.FormValue(
			"ansible_playbook",
		), Inventory: r.FormValue("ansible_inventory"),
		SSHKeyVariable: r.FormValue(
			"ansible_ssh_key_variable",
		), KnownHosts: r.FormValue("ansible_known_hosts"),
		Tags: parseStepVariableNames(
			r.FormValue("ansible_tags"),
		), Limit: parseStepVariableNames(r.FormValue("ansible_limit")),
	}
	if _, present := r.Form["ansible_check"]; present {
		return a, errors.New(
			"select Ansible check mode when starting an execution",
		)
	}
	if raw := strings.TrimSpace(r.FormValue("ansible_extra_vars")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &a.ExtraVars); err != nil {
			return a, errors.New(
				"extra variable mappings must be a JSON object of variable names",
			)
		}
	}
	return a, nil
}

func formInterpreter(r *http.Request) (string, error) {
	if r.FormValue("kind") == stepdefinition.AnsibleKind {
		return "", nil
	}
	return interpreter.Validate(r.FormValue("interpreter"))
}

func (h *StepHandler) PreviewAnsible(w http.ResponseWriter, r *http.Request) {
	audit.Suppress(r)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	a, err := formAnsible(r)
	var targets []string
	if err == nil && a == nil {
		err = errors.New("select an Ansible step")
	}
	if err == nil {
		targets, err = a.Targets()
	}
	message := ""
	if err != nil {
		message = err.Error()
	}
	if err := components.AnsibleTargets(targets, message).Render(r.Context(), w); err != nil {
		http.Error(
			w,
			"Cannot render target preview",
			http.StatusInternalServerError,
		)
	}
}

func (h *StepHandler) AnsibleCredentials(
	w http.ResponseWriter,
	r *http.Request,
) {
	projectID, ok := auth.ProjectIDFromContext(r.Context())
	if !ok {
		http.Error(w, "Invalid project ID", http.StatusBadRequest)
		return
	}
	credentials, err := h.repo.ListCredentials(r.Context(), projectID, false)
	if err != nil {
		http.Error(
			w,
			"Cannot load credential references",
			http.StatusInternalServerError,
		)
		return
	}
	names := []string{}
	for _, credential := range credentials {
		if credential.Kind != "package" && credential.Active {
			names = append(names, credential.Name)
		}
	}
	if err := components.AnsibleCredentialOptions(names).Render(r.Context(), w); err != nil {
		http.Error(
			w,
			"Cannot render credential references",
			http.StatusInternalServerError,
		)
	}
}
