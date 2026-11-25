package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/agentexecution"
	"durpdeploy/internal/containerenv"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/views/components"
	"durpdeploy/views/pages"
)

const (
	invalidProjectIDMessage = "Invalid project ID"
	invalidStepIDMessage    = "Invalid step ID"
)

// IsStepContainerRequired reports whether the execution target is the
// server-container path that requires a container image (local only;
// agent runs on the remote agent and must not carry one).
func IsStepContainerRequired(target string) bool {
	return target == "local"
}

// parseStepContainerConfig reads container_image and variable_names
// from the form and validates them against the chosen execution target.
// An empty error string means the values are valid; a non-empty
// message is rendered into the form with a 422 status.
func parseStepContainerConfig(
	r *http.Request,
	target string,
) (string, []string, string) {
	fields := formStepFields(r)
	if _, err := formAnsible(r); err != nil {
		return fields.ContainerImage, fields.VariableNames, err.Error()
	}
	fields.ExecutionTarget = target
	if err := ValidateStepExecutionFields(fields); err != nil {
		return fields.ContainerImage, fields.VariableNames, err.Error()
	}
	return fields.ContainerImage, fields.VariableNames, ""
}

func parseStepVariableNames(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	names := make([]string, 0, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		names = append(names, name)
	}
	return names
}

func validateStepContainerConfig(
	target, image string,
	variableNames []string,
) string {
	switch target {
	case "local":
		if image == "" {
			return "Container image is required for local steps"
		}
		if !ValidContainerImage(image) {
			return "Invalid container image"
		}
	default:
		if image != "" {
			return "Agent steps cannot use a container image"
		}
	}
	if len(variableNames) == 0 {
		return ""
	}
	seen := make(map[string]struct{}, len(variableNames))
	for _, name := range variableNames {
		switch containerenv.ValidateSelection(name, target == "local") {
		case containerenv.ErrIdentifier:
			return "Variable names must be identifiers: letters, digits, underscores, and cannot start with a digit"
		case containerenv.ErrReserved:
			return "Variable name is reserved for the container runner"
		}
		if _, dup := seen[name]; dup {
			return "Variable names contain duplicates"
		}
		seen[name] = struct{}{}
	}
	return ""
}

func ValidateStepExecutionConfig(config agentexecution.Config) string {
	mode, err := agentexecution.Parse(config)
	if err != nil {
		return err.Error()
	}
	target := config.Target
	if mode == "container" {
		target = "local"
	}
	return validateStepContainerConfig(
		target,
		config.Image,
		config.VariableNames,
	)
}

func marshalStepVariableNames(variableNames []string) string {
	names := variableNames
	if names == nil {
		names = []string{}
	}
	encoded, err := json.Marshal(names)
	if err != nil {
		return "[]"
	}
	return string(encoded)
}

type StepHandler struct {
	repo *repository.Repository
}

func (h *StepHandler) getProjectStep(
	r *http.Request,
	projectID, stepID int64,
) (db.Step, error) {
	step, err := h.repo.Queries.GetStep(r.Context(), stepID)
	if err == nil && step.ProjectID != projectID {
		err = sql.ErrNoRows
	}
	return step, err
}

func NewStepHandler(repo *repository.Repository) *StepHandler {
	return &StepHandler{repo: repo}
}

func (h *StepHandler) placementOptions(
	ctx context.Context,
	stepID int64,
) ([]string, []string, error) {
	labels, err := h.repo.Queries.ListAvailableAgentLabels(ctx)
	if err != nil {
		return nil, nil, err
	}
	if stepID == 0 {
		return labels, nil, nil
	}
	selected, err := h.repo.Queries.ListStepAgentSelectors(ctx, stepID)
	if err != nil {
		return nil, nil, err
	}
	return labels, selected, nil
}

func parseStepPlacement(
	r *http.Request,
	labels []string,
) (string, string, error) {
	target, selectors, err := ValidateStepPlacement(
		r.FormValue("execution_target"),
		[]string{r.FormValue("agent_label")},
		labels,
	)
	if err != nil {
		return "", "", err
	}
	if len(selectors) == 0 {
		return target, "", nil
	}
	return target, selectors[0], nil
}

func ValidateStepPlacement(
	target string,
	selectors, available []string,
) (string, []string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		target = "local"
	}
	if target != "local" && target != "agent" {
		//nolint:staticcheck // Preserve the public validation message.
		return "", nil, errors.New("Run step on must be Local or Agent")
	}
	if target == "local" {
		return target, nil, nil
	}
	canonical := make(map[string]string, len(available))
	for _, label := range available {
		canonical[strings.ToLower(label)] = label
	}
	validated := make([]string, 0, len(selectors))
	seen := make(map[string]struct{}, len(selectors))
	for _, selector := range selectors {
		selector = strings.ToLower(strings.TrimSpace(selector))
		if selector == "" {
			continue
		}
		label, ok := canonical[selector]
		if !ok {
			//nolint:staticcheck // Preserve the public validation message.
			return "", nil, errors.New("Select an available agent label")
		}
		key := strings.ToLower(label)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		validated = append(validated, label)
	}
	return target, validated, nil
}

func (h *StepHandler) ListSteps(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, invalidProjectIDMessage, http.StatusBadRequest)
		return
	}

	steps, err := h.repo.Queries.ListStepsByProject(r.Context(), projectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := components.StepList(steps, projectID).Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// StepsPage renders the dedicated full page for a project's steps. The
// project overview links here from its "Steps" button; the page hosts
// the same add/reorder/save-as-template UI that used to live inline on
// the project detail page.
func (h *StepHandler) StepsPage(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, invalidProjectIDMessage, http.StatusBadRequest)
		return
	}

	project, err := h.repo.Queries.GetProject(r.Context(), projectID)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Project not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	steps, err := h.repo.Queries.ListStepsByProject(r.Context(), projectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := pages.StepsPage(project, steps, r.URL.Path).
		Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *StepHandler) NewStepForm(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, invalidProjectIDMessage, http.StatusBadRequest)
		return
	}

	labels, _, err := h.placementOptions(r.Context(), 0)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	step := db.Step{ProjectID: projectID, ExecutionTarget: "local"}
	if err := components.StepForm(step, projectID, true, labels, "", "").
		Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *StepHandler) CreateStep(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, invalidProjectIDMessage, http.StatusBadRequest)
		return
	}
	labels, _, err := h.placementOptions(r.Context(), 0)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	target, agentLabel, placementErr := parseStepPlacement(r, labels)

	name := strings.TrimSpace(r.FormValue("name"))
	script := r.FormValue("script_body")
	selectedInterpreter, err := formInterpreter(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}

	formStep, variableNames, formError := h.prepareStepForm(r, db.Step{
		ProjectID: projectID, Name: name, ScriptBody: script,
		ExecutionTarget: r.FormValue("execution_target"),
	}, target, placementErr)
	if formError != "" {
		writeStepFormError(w, r, formStep, true, labels, agentLabel, formError)
		return
	}

	sortOrder, _ := strconv.ParseInt(r.FormValue("sort_order"), 10, 64)
	if sortOrder <= 0 {
		steps, err := h.repo.Queries.ListStepsByProject(r.Context(), projectID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for _, s := range steps {
			if s.SortOrder >= sortOrder {
				sortOrder = s.SortOrder + 1
			}
		}
	}

	fields := formStepFields(r)
	fields.Interpreter, fields.SortOrder = selectedInterpreter, sortOrder
	fields.TimeoutSeconds = formStep.TimeoutSeconds
	fields.MaxRetries = formStep.MaxRetries
	params := fields.CreateStepParams(
		projectID,
		marshalStepVariableNames(variableNames),
	)

	var selectors []string
	if agentLabel != "" {
		selectors = []string{agentLabel}
	}
	_, err = h.repo.CreateStepWithPlacement(
		r.Context(),
		params,
		target,
		selectors,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	steps, err := h.repo.Queries.ListStepsByProject(r.Context(), projectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := components.StepList(steps, projectID).Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *StepHandler) prepareStepForm(
	r *http.Request,
	step db.Step,
	target string,
	placementErr error,
) (db.Step, []string, string) {
	step, message := parseStepFormLimits(r, step)
	if message != "" {
		return step, nil, message
	}

	image, names, message := parseStepContainerConfig(r, target)
	if a, _ := formAnsible(r); a != nil && message == "" {
		if err := h.repo.ValidateAnsibleReferences(r.Context(), step.ProjectID, a); err != nil {
			message = err.Error()
		}
	}
	if err := agentexecution.ValidateLimitsWithPolicy(
		step.TimeoutSeconds, step.MaxRetries, h.repo.ResourceLimits().Execution,
	); err != nil {
		message = err.Error()
	}
	if message == "" {
		switch {
		case step.Name == "":
			message = "Name is required"
		case placementErr != nil:
			message = placementErr.Error()
		default:
			return step, names, ""
		}
	}

	step.ContainerImage = image
	step.AgentExecutionMode = r.FormValue("agent_execution_mode")
	step.NetworkMode = r.FormValue("network_mode")
	step.ApprovalArtifactPath = r.FormValue("approval_artifact_path")
	step.ApprovalReviewPath = r.FormValue("approval_review_path")
	step.ApprovalReviewFormat = r.FormValue("approval_review_format")
	step.VariableNames = marshalStepVariableNames(names)
	return step, names, message
}

func parseStepFormLimits(r *http.Request, step db.Step) (db.Step, string) {
	timeout, ok := parseStepFormLimit(r.FormValue("timeout_seconds"))
	if !ok {
		return step, "Timeout must be a non-negative integer"
	}
	step.TimeoutSeconds = timeout
	retries, ok := parseStepFormLimit(r.FormValue("max_retries"))
	if !ok {
		return step, "Max retries must be a non-negative integer"
	}
	step.MaxRetries = retries
	return step, ""
}

func parseStepFormLimit(raw string) (int64, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, true
	}
	limit, err := strconv.ParseInt(value, 10, 64)
	return limit, err == nil && limit >= 0
}

// writeStepFormError centralizes the StepForm/StepEditRow re-render path
// for HTMX vs. full-page 422 responses. Both branches render the same
// form so the displayed input values match what the user submitted.
func writeStepFormError(
	w http.ResponseWriter,
	r *http.Request,
	step db.Step,
	isNew bool,
	labels []string,
	agentLabel, errorMsg string,
) {
	a, _ := formAnsible(r)
	step.Kind, step.AnsibleJson = r.FormValue("kind"), a.JSON()
	if r.FormValue("dialog") == "1" {
		step.Interpreter = r.FormValue("interpreter")
		step.ContainerImage = r.FormValue("container_image")
		step.NetworkMode = r.FormValue("network_mode")
		step.ApprovalArtifactPath = r.FormValue("approval_artifact_path")
		step.ApprovalReviewPath = r.FormValue("approval_review_path")
		step.ApprovalReviewFormat = r.FormValue("approval_review_format")
		_, names, _ := parseStepContainerConfig(r, step.ExecutionTarget)
		step.VariableNames = marshalStepVariableNames(names)
		w.Header().Set(hxRetargetHeader, "#step-edit-content")
		w.Header().Set("HX-Reswap", "innerHTML")
		w.WriteHeader(http.StatusUnprocessableEntity)
		if err := components.StepForm(
			step, step.ProjectID, isNew, labels, agentLabel, errorMsg,
		).Render(r.Context(), w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	mobile := r.URL.Query().Get("mobile") == "1"
	if r.Header.Get("HX-Request") == "true" {
		if isNew {
			w.Header().Set(hxRetargetHeader, "#add-step-form")
		} else if mobile {
			w.Header().Set(hxRetargetHeader,
				fmt.Sprintf("#mobile-step-edit-%d", step.ID))
		} else {
			w.Header().Set(hxRetargetHeader, fmt.Sprintf("#step-row-%d", step.ID))
			w.Header().Set("HX-Reswap", "outerHTML")
		}
	}
	if isNew || mobile {
		WriteFormError(
			w,
			r,
			components.StepForm(
				step, step.ProjectID, isNew, labels, agentLabel, errorMsg,
			),
			components.StepForm(
				step, step.ProjectID, isNew, labels, agentLabel, errorMsg,
			),
		)
		return
	}
	WriteFormError(
		w,
		r,
		components.StepEditRow(
			step, step.ProjectID, labels, agentLabel, errorMsg,
		),
		components.StepEditRow(
			step, step.ProjectID, labels, agentLabel, errorMsg,
		),
	)
}

func (h *StepHandler) EditStepForm(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, invalidProjectIDMessage, http.StatusBadRequest)
		return
	}

	stepIDStr := chi.URLParam(r, "stepId")
	stepID, err := strconv.ParseInt(stepIDStr, 10, 64)
	if err != nil {
		http.Error(w, invalidStepIDMessage, http.StatusBadRequest)
		return
	}

	step, err := h.getProjectStep(r, projectID, stepID)
	if err != nil {
		if err == sql.ErrNoRows {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	labels, selected, err := h.placementOptions(r.Context(), step.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	selectedLabel := ""
	if len(selected) > 0 {
		selectedLabel = selected[0]
	}
	if r.URL.Query().Get("dialog") == "1" ||
		r.URL.Query().Get("mobile") == "1" {
		if err := components.StepForm(
			step, projectID, false, labels, selectedLabel, "",
		).Render(r.Context(), w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	if err := components.StepEditRow(step, projectID, labels, selectedLabel, "").
		Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *StepHandler) UpdateStep(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, invalidProjectIDMessage, http.StatusBadRequest)
		return
	}

	stepIDStr := chi.URLParam(r, "stepId")
	stepID, err := strconv.ParseInt(stepIDStr, 10, 64)
	if err != nil {
		http.Error(w, invalidStepIDMessage, http.StatusBadRequest)
		return
	}
	existing, err := h.getProjectStep(r, projectID, stepID)
	if err != nil {
		if err == sql.ErrNoRows {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Legacy local steps were created before the server-container
	// requirement and cannot be upgraded in place: the runner refuses
	// to start them, and the user must recreate the step with an image.
	if existing.ExecutionTarget == "local" && existing.ContainerImage == "" {
		writeStepFormError(
			w,
			r,
			existing,
			false,
			nil,
			"",
			"Legacy local step cannot be edited. Recreate it with a container image instead.",
		)
		return
	}
	labels, selected, err := h.placementOptions(r.Context(), stepID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	selectedLabel := ""
	if len(selected) > 0 {
		selectedLabel = selected[0]
	}
	target, agentLabel, placementErr := parseStepPlacement(r, labels)

	name := strings.TrimSpace(r.FormValue("name"))
	script := r.FormValue("script_body")
	selectedInterpreter, err := formInterpreter(r)
	if err != nil {
		step := existing
		step.Name, step.ScriptBody = name, script
		writeStepFormError(
			w, r, step, false, labels, agentLabel, err.Error(),
		)
		return
	}
	sortOrder, _ := strconv.ParseInt(r.FormValue("sort_order"), 10, 64)

	formStep, variableNames, formError := h.prepareStepForm(r, db.Step{
		ID: stepID, ProjectID: projectID, Name: name, ScriptBody: script, SortOrder: sortOrder,
		ExecutionTarget: r.FormValue("execution_target"),
	}, target, placementErr)
	if formError != "" {
		writeStepFormError(w, r, formStep, false, labels, agentLabel, formError)
		return
	}

	fields := formStepFields(r)
	fields.Interpreter, fields.SortOrder = selectedInterpreter, sortOrder
	fields.TimeoutSeconds = formStep.TimeoutSeconds
	fields.MaxRetries = formStep.MaxRetries
	params := fields.UpdateStepParams(
		stepID,
		marshalStepVariableNames(variableNames),
	)

	var selectors []string
	if agentLabel != "" {
		selectors = []string{agentLabel}
	}
	if target == "agent" && agentLabel == selectedLabel {
		selectors = selected
	}
	_, err = h.repo.UpdateStepWithPlacement(
		r.Context(), params, target, selectors,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	steps, err := h.repo.Queries.ListStepsByProject(r.Context(), projectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := components.StepList(steps, projectID).Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *StepHandler) DeleteStep(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, invalidProjectIDMessage, http.StatusBadRequest)
		return
	}

	stepIDStr := chi.URLParam(r, "stepId")
	stepID, err := strconv.ParseInt(stepIDStr, 10, 64)
	if err != nil {
		http.Error(w, invalidStepIDMessage, http.StatusBadRequest)
		return
	}
	if _, err := h.getProjectStep(r, projectID, stepID); err != nil {
		if err == sql.ErrNoRows {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := h.repo.Queries.DeleteStep(r.Context(), stepID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		SetToastSuccess(w, "Step deleted")
	}

	steps, err := h.repo.Queries.ListStepsByProject(r.Context(), projectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := components.StepList(steps, projectID).Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *StepHandler) ReorderStep(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, invalidProjectIDMessage, http.StatusBadRequest)
		return
	}

	stepID, err := strconv.ParseInt(r.FormValue("step_id"), 10, 64)
	if err != nil {
		http.Error(w, invalidStepIDMessage, http.StatusBadRequest)
		return
	}

	newOrder, err := strconv.ParseInt(r.FormValue("new_order"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid new order", http.StatusBadRequest)
		return
	}

	steps, err := h.repo.Queries.ListStepsByProject(r.Context(), projectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var target db.Step
	found := false
	for _, s := range steps {
		if s.ID == stepID {
			target = s
			found = true
			break
		}
	}
	if !found {
		http.Error(w, "Step not found", http.StatusNotFound)
		return
	}

	if target.SortOrder == newOrder {
		if err := components.StepList(steps, projectID).Render(r.Context(), w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	tx, err := h.repo.DB.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer func() {
		repository.LogRollbackError(tx.Rollback())
	}()

	if err := reorderStepRows(
		r.Context(), h.repo.Queries.WithTx(tx), steps, target, newOrder,
	); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	steps, err = h.repo.Queries.ListStepsByProject(r.Context(), projectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := components.StepList(steps, projectID).Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func reorderStepRows(
	ctx context.Context,
	q *db.Queries,
	steps []db.Step,
	target db.Step,
	newOrder int64,
) error {
	oldOrder := target.SortOrder
	for _, step := range steps {
		if step.ID == target.ID {
			continue
		}
		order := step.SortOrder
		switch {
		case newOrder < oldOrder && order >= newOrder && order < oldOrder:
			order++
		case newOrder > oldOrder && order > oldOrder && order <= newOrder:
			order--
		default:
			continue
		}
		if err := updateStepOrder(ctx, q, step, order); err != nil {
			return err
		}
	}
	return updateStepOrder(ctx, q, target, newOrder)
}

func updateStepOrder(
	ctx context.Context,
	q *db.Queries,
	step db.Step,
	newOrder int64,
) error {
	_, err := q.UpdateStep(ctx, db.UpdateStepParams{
		ID:   step.ID,
		Name: step.Name,
		Kind: step.Kind, AnsibleJson: step.AnsibleJson,
		ScriptBody:           step.ScriptBody,
		SortOrder:            newOrder,
		TimeoutSeconds:       step.TimeoutSeconds,
		MaxRetries:           step.MaxRetries,
		Interpreter:          step.Interpreter,
		ContainerImage:       step.ContainerImage,
		AgentExecutionMode:   step.AgentExecutionMode,
		NetworkMode:          step.NetworkMode,
		ApprovalArtifactPath: step.ApprovalArtifactPath,
		ApprovalReviewPath:   step.ApprovalReviewPath,
		ApprovalReviewFormat: step.ApprovalReviewFormat,
		VariableNames:        step.VariableNames,
	})
	return err
}
