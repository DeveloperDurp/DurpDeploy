package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"durpdeploy/internal/db"
	"durpdeploy/internal/interpreter"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
)

type RunbookHandler struct {
	repo   *repository.Repository
	runner *runner.DeploymentRunner
}

func NewRunbookHandler(
	repo *repository.Repository,
	r *runner.DeploymentRunner,
) *RunbookHandler {
	return &RunbookHandler{repo: repo, runner: r}
}

type runbookStep struct {
	Name            string   `json:"name"`
	ScriptBody      string   `json:"script_body"`
	Interpreter     string   `json:"interpreter"`
	SortOrder       int      `json:"sort_order"`
	TimeoutSeconds  int64    `json:"timeout_seconds"`
	MaxRetries      int64    `json:"max_retries"`
	ExecutionTarget string   `json:"execution_target"`
	AgentSelectors  []string `json:"agent_selectors,omitempty"`
}

type runbookSaveRequest struct {
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Steps       []runbookStep `json:"steps"`
}

// swagger:route GET /projects/{id}/runbooks runbooks listRunbooks
// List project runbooks.
func (h *RunbookHandler) List(w http.ResponseWriter, r *http.Request) {
	projectID, ok := requireProjectFromContext(w, r)
	if !ok {
		return
	}
	items, err := h.repo.Queries.ListRunbooks(r.Context(), projectID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "Cannot list runbooks")
		return
	}
	RespondJSON(w, http.StatusOK, items)
}

// swagger:route GET /projects/{id}/runbooks/{runbookId} runbooks getRunbook
// Read a runbook and its saved versions.
func (h *RunbookHandler) Get(w http.ResponseWriter, r *http.Request) {
	projectID, ok := requireProjectFromContext(w, r)
	if !ok {
		return
	}
	id, ok := parseIDParam(w, r, "runbookId")
	if !ok {
		return
	}
	book, err := h.repo.Queries.GetRunbook(r.Context(), db.GetRunbookParams{
		ID: id, ProjectID: projectID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		RespondError(w, http.StatusNotFound, "Runbook not found")
		return
	}
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "Cannot read runbook")
		return
	}
	versions, err := h.repo.Queries.ListRunbookVersions(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "Cannot list versions")
		return
	}
	RespondJSON(w, http.StatusOK, struct {
		Runbook  db.Runbook          `json:"runbook"`
		Versions []db.RunbookVersion `json:"versions"`
	}{book, versions})
}

// swagger:route GET /projects/{id}/runbooks/{runbookId}/versions/{versionId} runbooks getRunbookVersion
// Read an immutable runbook version.
func (h *RunbookHandler) Version(w http.ResponseWriter, r *http.Request) {
	projectID, ok := requireProjectFromContext(w, r)
	if !ok {
		return
	}
	id, ok := parseIDParam(w, r, "runbookId")
	if !ok {
		return
	}
	versionID, ok := parseIDParam(w, r, "versionId")
	if !ok {
		return
	}
	if _, err := h.repo.Queries.GetRunbook(r.Context(), db.GetRunbookParams{
		ID: id, ProjectID: projectID,
	}); err != nil {
		RespondError(w, http.StatusNotFound, "Runbook not found")
		return
	}
	version, err := h.repo.Queries.GetRunbookVersion(
		r.Context(),
		db.GetRunbookVersionParams{
			ID: versionID, RunbookID: id,
		},
	)
	if err != nil {
		RespondError(w, http.StatusNotFound, "Version not found")
		return
	}
	release, err := h.repo.Queries.GetRelease(r.Context(), version.ReleaseID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "Cannot read version")
		return
	}
	RespondJSON(w, http.StatusOK, struct {
		Version db.RunbookVersion `json:"version"`
		Steps   json.RawMessage   `json:"steps"`
	}{version, json.RawMessage(release.StepsJson)})
}

// swagger:route PUT /projects/{id}/runbooks/{runbookId} runbooks saveRunbookVersion
// Save the next immutable version of a runbook.
func (h *RunbookHandler) Save(w http.ResponseWriter, r *http.Request) {
	projectID, ok := requireProjectFromContext(w, r)
	if !ok {
		return
	}
	var req runbookSaveRequest
	if !readJSONBool(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if len(req.Steps) == 0 || (r.Method == http.MethodPost && req.Name == "") {
		RespondError(
			w,
			http.StatusUnprocessableEntity,
			"Name and steps are required",
		)
		return
	}
	for i := range req.Steps {
		step := &req.Steps[i]
		step.Name = strings.TrimSpace(step.Name)
		if step.Name == "" || strings.TrimSpace(step.ScriptBody) == "" ||
			step.TimeoutSeconds < 0 || step.MaxRetries < 0 {
			RespondError(
				w,
				http.StatusUnprocessableEntity,
				"Invalid runbook step",
			)
			return
		}
		selected, err := interpreter.Validate(step.Interpreter)
		if err != nil {
			RespondError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		step.Interpreter = selected
		target, selectors, valid := validatePlacement(
			w, r, h.repo, step.ExecutionTarget, step.AgentSelectors,
		)
		if !valid {
			return
		}
		step.ExecutionTarget = target
		step.AgentSelectors = selectors
		step.SortOrder = i
	}
	steps, err := json.Marshal(req.Steps)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "Cannot encode steps")
		return
	}
	var id int64
	if r.Method != http.MethodPost {
		id, ok = parseIDParam(w, r, "runbookId")
		if !ok {
			return
		}
	}
	book, version, err := h.repo.SaveRunbook(
		r.Context(),
		repository.RunbookSave{
			ProjectID: projectID, RunbookID: id, Name: req.Name,
			Description: req.Description, StepsJSON: string(steps),
		},
	)
	if errors.Is(err, sql.ErrNoRows) {
		RespondError(w, http.StatusNotFound, "Runbook not found")
		return
	}
	if isUniqueViolation(err) {
		RespondError(
			w,
			http.StatusConflict,
			"Runbook name or version already exists",
		)
		return
	}
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "Cannot save runbook")
		return
	}
	RespondJSON(w, http.StatusCreated, struct {
		Runbook db.Runbook        `json:"runbook"`
		Version db.RunbookVersion `json:"version"`
	}{book, version})
}

// swagger:route POST /projects/{id}/runbooks runbooks createRunbook
// Create a runbook and its first immutable version.
func (h *RunbookHandler) Create(w http.ResponseWriter, r *http.Request) {
	h.Save(w, r)
}
