package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"durpdeploy/internal/audit"
	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/views/pages"
	"github.com/go-chi/chi/v5"
)

const credentialInvalidForm = "Invalid form"

type CredentialHandler struct {
	repo      *repository.Repository
	lifecycle bool
}

func NewCredentialHandler(
	repo *repository.Repository,
	lifecycle bool,
) *CredentialHandler {
	return &CredentialHandler{repo: repo, lifecycle: lifecycle}
}

func CredentialErrorStatus(err error) int {
	if IsUniqueViolation(err) {
		return 422
	}
	switch {
	case errors.Is(err, repository.ErrCredentialInput),
		errors.Is(err, repository.ErrCredentialAmbiguous):
		return 422
	case errors.Is(err, repository.ErrCredentialConflict):
		return 409
	case errors.Is(err, sql.ErrNoRows):
		return 404
	default:
		return 500
	}
}

func CredentialErrorMessage(err error) string {
	if errors.Is(err, repository.ErrCredentialAmbiguous) {
		return "Multiple variables share this name and environment. Remove duplicates before rotation."
	}
	switch CredentialErrorStatus(err) {
	case 422:
		return "Use a valid, unique name and environment with valid dates. Enter a nonempty replacement when rotating. Metadata saves must leave the replacement blank."
	case 409:
		return "The credential changed. Reload the page before saving."
	case 404:
		return "Credential not found"
	default:
		return "Unable to save credential changes"
	}
}

// AuditCredentialResult uses the middleware for successful writes. Failed
// attempts need an explicit event because middleware omits 4xx/5xx responses.
func AuditCredentialResult(r *http.Request, repo *repository.Repository,
	kind string, id int64, rotate bool, err error,
) {
	action := "update_credential_metadata"
	if rotate {
		action = "rotate_credential"
	}
	AuditCredentialWriteResult(r, repo, kind, id, action, err)
}

func AuditCredentialWriteResult(
	r *http.Request,
	repo *repository.Repository,
	kind string,
	id int64,
	action string,
	err error,
) {
	if err == nil {
		audit.SetProjectTemplateResult(r, action, kind+"_credential", id)
		return
	}
	userID := sql.NullInt64{}
	if user := auth.UserFromContext(r.Context()); user != nil {
		userID = sql.NullInt64{Int64: user.ID, Valid: true}
	}
	// Do not include provider errors, submitted values, or arbitrary kinds.
	if kind != "variable" && kind != "lifecycle" && kind != "package" {
		kind = "unknown"
	}
	details, _ := json.Marshal(map[string]any{
		"kind": kind,
		"owner_id": chi.URLParam(
			r,
			"id",
		),
		"status": CredentialErrorStatus(err),
	})
	audit.Record(r.Context(), repo, audit.Entry{
		UserID:     userID,
		Action:     action + "_failed",
		EntityType: kind + "_credential",
		EntityID: sql.NullInt64{
			Int64: id,
			Valid: id > 0,
		},
		Details: string(details),
	})
}

func (h *CredentialHandler) page(w http.ResponseWriter, r *http.Request,
	message string, status int,
) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "Invalid owner ID", 400)
		return
	}
	rows, err := h.repo.ListCredentials(r.Context(), id, h.lifecycle)
	if err != nil {
		http.Error(w, CredentialErrorMessage(err), CredentialErrorStatus(err))
		return
	}
	data := pages.CredentialsData{
		Credentials: rows,
		OwnerID:     id,
		Lifecycle:   h.lifecycle,
		Error:       message,
		Saved:       r.URL.Query().Get("saved") == "1",
	}
	data.Mode = credentialPageMode(r)
	if data.Mode == "edit" || data.Mode == "delete" {
		if !selectCredentialCard(&data, r) {
			http.NotFound(w, r)
			return
		}
	}
	if err := h.loadCredentialOwner(r, &data); err != nil {
		http.Error(w, "Unable to load credential owner", 500)
		return
	}
	if data.Mode == "new" {
		data.Candidates, err = h.repo.CredentialCandidates(
			r.Context(),
			id,
			h.lifecycle,
		)
		if err == nil {
			data.Environments, err = h.credentialEnvironments(r, id)
		}
		if err != nil {
			http.Error(w, "Unable to load credential form", 500)
			return
		}
	}
	if status != 200 {
		w.WriteHeader(status)
	}
	if err := pages.CredentialsPage(data, r.URL.Path).
		Render(r.Context(), w); err != nil {
		http.Error(w, "Unable to render credentials", 500)
	}
}

func credentialPageMode(r *http.Request) string {
	for _, mode := range []string{"new", "edit", "delete"} {
		if strings.HasSuffix(r.URL.Path, "/"+mode) {
			return mode
		}
	}
	if r.Method != http.MethodPost {
		return ""
	}
	if chi.URLParam(r, "credentialId") != "" {
		return "edit"
	}
	return "new"
}

func selectCredentialCard(data *pages.CredentialsData, r *http.Request) bool {
	id, err := strconv.ParseInt(chi.URLParam(r, "credentialId"), 10, 64)
	if err != nil {
		return false
	}
	for _, row := range data.Credentials {
		if row.ID == id && row.Kind == chi.URLParam(r, "kind") &&
			!row.Inherited {
			data.Credentials = []repository.Credential{row}
			return true
		}
	}
	return false
}

func (h *CredentialHandler) loadCredentialOwner(
	r *http.Request,
	data *pages.CredentialsData,
) error {
	if !h.lifecycle {
		owner, err := h.repo.Queries.GetProject(r.Context(), data.OwnerID)
		data.Name = owner.Name
		return err
	}
	owner, err := h.repo.Queries.GetLifecycle(r.Context(), data.OwnerID)
	if err != nil {
		return err
	}
	data.Name = owner.Name
	projects, err := h.repo.Queries.ListProjectsByLifecycle(
		r.Context(),
		sql.NullInt64{Int64: data.OwnerID, Valid: true},
	)
	for _, project := range projects {
		data.Projects = append(
			data.Projects,
			pages.CredentialProject{ID: project.ID, Name: project.Name},
		)
	}
	return err
}

func (h *CredentialHandler) credentialEnvironments(
	r *http.Request,
	id int64,
) ([]db.Environment, error) {
	if !h.lifecycle {
		owner, err := h.repo.Queries.GetProject(r.Context(), id)
		if err != nil {
			return nil, err
		}
		_, options, err := h.repo.VariableEnvironments(r.Context(), owner)
		return options, err
	}
	environments, err := h.repo.Queries.ListEnvironments(r.Context())
	if err != nil {
		return nil, err
	}
	stages, err := h.repo.Queries.ListLifecycleStageEnvironmentIDs(
		r.Context(),
		id,
	)
	if err != nil {
		return nil, err
	}
	allowed := map[int64]bool{}
	for _, stage := range stages {
		allowed[stage] = true
	}
	options := []db.Environment{}
	for _, environment := range environments {
		if allowed[environment.ID] {
			options = append(options, environment)
		}
	}
	return options, nil
}

func (h *CredentialHandler) List(w http.ResponseWriter, r *http.Request) {
	h.page(w, r, "", 200)
}

func (h *CredentialHandler) redirect(
	w http.ResponseWriter,
	r *http.Request,
	ownerID int64,
) {
	owner := "projects"
	if h.lifecycle {
		owner = "lifecycles"
	}
	http.Redirect(
		w,
		r,
		fmt.Sprintf("/%s/%d/credentials?saved=1", owner, ownerID),
		http.StatusSeeOther,
	)
}

func (h *CredentialHandler) Create(w http.ResponseWriter, r *http.Request) {
	if r.ParseForm() != nil {
		http.Error(w, credentialInvalidForm, 400)
		return
	}
	ownerID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	input := repository.CredentialCreateInput{
		Name:  r.FormValue("name"),
		Value: r.FormValue("value"),
	}
	if err == nil {
		input.ExpiresAt, err = credentialFormTime(r.FormValue("expires_at"))
	}
	if err == nil {
		input.RotationDueAt, err = credentialFormTime(
			r.FormValue("rotation_due_at"),
		)
	}
	if err == nil && r.FormValue("environment_id") != "" {
		var id int64
		id, err = strconv.ParseInt(r.FormValue("environment_id"), 10, 64)
		input.EnvironmentID = &id
	}
	var result repository.Credential
	if err == nil {
		result, err = h.repo.CreateCredential(
			r.Context(),
			ownerID,
			h.lifecycle,
			input,
		)
	} else {
		err = repository.ErrCredentialInput
	}
	AuditCredentialWriteResult(
		r,
		h.repo,
		h.variableKind(),
		result.ID,
		"create_credential",
		err,
	)
	if err != nil {
		h.page(w, r, CredentialErrorMessage(err), CredentialErrorStatus(err))
		return
	}
	h.redirect(w, r, ownerID)
}

func (h *CredentialHandler) variableKind() string {
	if h.lifecycle {
		return "lifecycle"
	}
	return "variable"
}

func (h *CredentialHandler) Enroll(w http.ResponseWriter, r *http.Request) {
	if r.ParseForm() != nil {
		http.Error(w, credentialInvalidForm, 400)
		return
	}
	ownerID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	input := repository.CredentialEnrollmentInput{}
	if err == nil {
		input.VariableID, err = strconv.ParseInt(
			r.FormValue("variable_id"),
			10,
			64,
		)
	}
	if err == nil {
		input.Revision, err = strconv.ParseInt(r.FormValue("revision"), 10, 64)
	}
	if err == nil {
		_, err = h.repo.EnrollCredential(
			r.Context(),
			ownerID,
			h.lifecycle,
			input,
		)
	} else {
		err = repository.ErrCredentialInput
	}
	AuditCredentialWriteResult(
		r,
		h.repo,
		h.variableKind(),
		input.VariableID,
		"enroll_credential",
		err,
	)
	if err != nil {
		h.page(w, r, CredentialErrorMessage(err), CredentialErrorStatus(err))
		return
	}
	h.redirect(w, r, ownerID)
}

func (h *CredentialHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if r.ParseForm() != nil {
		http.Error(w, credentialInvalidForm, 400)
		return
	}
	ownerID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var id, revision int64
	if err == nil {
		id, err = strconv.ParseInt(chi.URLParam(r, "credentialId"), 10, 64)
	}
	if err == nil {
		revision, err = strconv.ParseInt(r.FormValue("revision"), 10, 64)
	}
	if err == nil {
		err = h.repo.DeleteCredential(
			r.Context(),
			ownerID,
			h.lifecycle,
			chi.URLParam(r, "kind"),
			id,
			revision,
		)
	} else {
		err = repository.ErrCredentialInput
	}
	AuditCredentialWriteResult(
		r,
		h.repo,
		chi.URLParam(r, "kind"),
		id,
		"delete_credential",
		err,
	)
	if err != nil {
		h.page(w, r, CredentialErrorMessage(err), CredentialErrorStatus(err))
		return
	}
	h.redirect(w, r, ownerID)
}

func credentialFormTime(value string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	t, err := time.Parse("2006-01-02T15:04", value)
	if err != nil {
		return 0, repository.ErrCredentialInput
	}
	return t.Unix(), nil
}

func (h *CredentialHandler) Save(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, credentialInvalidForm, 400)
		return
	}
	ownerID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || ownerID <= 0 {
		http.Error(w, "Invalid owner ID", 400)
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "credentialId"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "Invalid credential ID", 400)
		return
	}
	input := repository.CredentialRotationInput{Value: r.FormValue("value")}
	input.Revision, err = strconv.ParseInt(r.FormValue("revision"), 10, 64)
	if err == nil {
		input.ExpiresAt, err = credentialFormTime(r.FormValue("expires_at"))
	}
	if err == nil {
		input.RotationDueAt, err = credentialFormTime(
			r.FormValue("rotation_due_at"),
		)
	}
	rotate := input.Value != ""
	if err == nil {
		_, err = h.repo.SaveCredentialRotation(
			r.Context(),
			ownerID,
			h.lifecycle,
			chi.URLParam(r, "kind"),
			id,
			input,
			rotate,
		)
	} else {
		err = repository.ErrCredentialInput
	}
	AuditCredentialResult(r, h.repo, chi.URLParam(r, "kind"), id, rotate, err)
	if err != nil {
		h.page(w, r, CredentialErrorMessage(err), CredentialErrorStatus(err))
		return
	}
	owner := "projects"
	if h.lifecycle {
		owner = "lifecycles"
	}
	http.Redirect(
		w,
		r,
		fmt.Sprintf(
			"/%s/%d/credentials/%s/%d/edit?saved=1",
			owner,
			ownerID,
			chi.URLParam(r, "kind"),
			id,
		),
		http.StatusSeeOther,
	)
}
