package api

import (
	"net/http"
	"strings"

	"durpdeploy/internal/handler"
	"durpdeploy/internal/repository"
	"github.com/go-chi/chi/v5"
)

const credentialInvalidOwnerID = "Invalid owner ID"

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

func (h *CredentialHandler) List(w http.ResponseWriter, r *http.Request) {
	id, err := parseParamInt(r, "id")
	if err != nil || id <= 0 {
		RespondError(w, 400, credentialInvalidOwnerID)
		return
	}
	rows, err := h.repo.ListCredentials(r.Context(), id, h.lifecycle)
	if err != nil {
		RespondError(
			w,
			handler.CredentialErrorStatus(err),
			handler.CredentialErrorMessage(err),
		)
		return
	}
	RespondJSON(w, 200, rows)
}

func (h *CredentialHandler) Status(w http.ResponseWriter, r *http.Request) {
	id, err := parseParamInt(r, "id")
	if err != nil || id <= 0 {
		RespondError(w, 400, credentialInvalidOwnerID)
		return
	}
	rows, err := h.repo.ListCredentials(r.Context(), id, h.lifecycle)
	if err != nil {
		RespondError(
			w,
			handler.CredentialErrorStatus(err),
			handler.CredentialErrorMessage(err),
		)
		return
	}
	RespondJSON(w, 200, repository.SummarizeCredentials(rows))
}

func (h *CredentialHandler) Candidates(w http.ResponseWriter, r *http.Request) {
	id, err := parseParamInt(r, "id")
	if err != nil || id <= 0 {
		RespondError(w, 400, credentialInvalidOwnerID)
		return
	}
	if _, err = h.repo.ListCredentials(
		r.Context(),
		id,
		h.lifecycle,
	); err != nil {
		RespondError(
			w,
			handler.CredentialErrorStatus(err),
			handler.CredentialErrorMessage(err),
		)
		return
	}
	rows, err := h.repo.CredentialCandidates(r.Context(), id, h.lifecycle)
	if err != nil {
		RespondError(
			w,
			handler.CredentialErrorStatus(err),
			handler.CredentialErrorMessage(err),
		)
		return
	}
	RespondJSON(w, 200, rows)
}

func (h *CredentialHandler) Create(w http.ResponseWriter, r *http.Request) {
	id, err := parseParamInt(r, "id")
	if err != nil || id <= 0 {
		RespondError(w, 400, credentialInvalidOwnerID)
		return
	}
	var input repository.CredentialCreateInput
	if !readJSONBool(w, r, &input) {
		return
	}
	result, err := h.repo.CreateCredential(r.Context(), id, h.lifecycle, input)
	handler.AuditCredentialWriteResult(
		r,
		h.repo,
		h.variableKind(),
		result.ID,
		"create_credential",
		err,
	)
	if err != nil {
		RespondError(
			w,
			handler.CredentialErrorStatus(err),
			handler.CredentialErrorMessage(err),
		)
		return
	}
	RespondJSON(w, 201, result)
}

func (h *CredentialHandler) variableKind() string {
	if h.lifecycle {
		return "lifecycle"
	}
	return "variable"
}

func (h *CredentialHandler) Enroll(w http.ResponseWriter, r *http.Request) {
	id, err := parseParamInt(r, "id")
	if err != nil || id <= 0 {
		RespondError(w, 400, credentialInvalidOwnerID)
		return
	}
	var input repository.CredentialEnrollmentInput
	if !readJSONBool(w, r, &input) {
		return
	}
	result, err := h.repo.EnrollCredential(r.Context(), id, h.lifecycle, input)
	handler.AuditCredentialWriteResult(
		r,
		h.repo,
		h.variableKind(),
		input.VariableID,
		"enroll_credential",
		err,
	)
	if err != nil {
		RespondError(
			w,
			handler.CredentialErrorStatus(err),
			handler.CredentialErrorMessage(err),
		)
		return
	}
	RespondJSON(w, 200, result)
}

func (h *CredentialHandler) Delete(w http.ResponseWriter, r *http.Request) {
	ownerID, err := parseParamInt(r, "id")
	if err != nil || ownerID <= 0 {
		RespondError(w, 400, credentialInvalidOwnerID)
		return
	}
	id, err := parseParamInt(r, "credentialId")
	if err != nil || id <= 0 {
		RespondError(w, 400, "Invalid credential ID")
		return
	}
	var input struct {
		Revision *int64 `json:"revision"`
	}
	if !readJSONBool(w, r, &input) {
		return
	}
	if input.Revision == nil {
		RespondError(w, 422, "Revision is required")
		return
	}
	err = h.repo.DeleteCredential(
		r.Context(),
		ownerID,
		h.lifecycle,
		chi.URLParam(r, "kind"),
		id,
		*input.Revision,
	)
	handler.AuditCredentialWriteResult(
		r,
		h.repo,
		chi.URLParam(r, "kind"),
		id,
		"delete_credential",
		err,
	)
	if err != nil {
		RespondError(
			w,
			handler.CredentialErrorStatus(err),
			handler.CredentialErrorMessage(err),
		)
		return
	}
	w.WriteHeader(204)
}

func (h *CredentialHandler) Save(w http.ResponseWriter, r *http.Request) {
	ownerID, err := parseParamInt(r, "id")
	if err != nil || ownerID <= 0 {
		RespondError(w, 400, credentialInvalidOwnerID)
		return
	}
	id, err := parseParamInt(r, "credentialId")
	if err != nil || id <= 0 {
		RespondError(w, 400, "Invalid credential ID")
		return
	}
	var input repository.CredentialRotationInput
	if !readJSONBool(w, r, &input) {
		return
	}
	rotate := input.Value != "" || strings.HasSuffix(r.URL.Path, "/rotate")
	result, err := h.repo.SaveCredentialRotation(r.Context(), ownerID,
		h.lifecycle, chi.URLParam(r, "kind"), id, input, rotate)
	handler.AuditCredentialResult(
		r,
		h.repo,
		chi.URLParam(r, "kind"),
		id,
		rotate,
		err,
	)
	if err != nil {
		RespondError(
			w,
			handler.CredentialErrorStatus(err),
			handler.CredentialErrorMessage(err),
		)
		return
	}
	RespondJSON(w, 200, result)
}
