package handler

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/repository"
	"durpdeploy/views/pages"
)

func (h *VariableHandler) EditVariableGroup(
	w http.ResponseWriter, r *http.Request,
) {
	projectID, anchorID, ok := parseVariableGroupPath(w, r)
	if !ok {
		return
	}
	if !isFragmentRequest(r) {
		redirectToVariableList(w, r, projectID)
		return
	}
	if !pages.CanWrite(r.Context()) {
		http.Error(w, "You cannot edit variable values", http.StatusForbidden)
		return
	}
	h.renderVariableGroup(w, r, projectID, anchorID, http.StatusOK, "")
}

func (h *VariableHandler) SaveVariableGroup(
	w http.ResponseWriter, r *http.Request,
) {
	projectID, anchorID, ok := parseVariableGroupPath(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}
	values, err := parseVariableGroupValues(r.PostForm)
	if err == nil {
		err = h.repo.UpdateVariableGroup(
			r.Context(),
			projectID,
			anchorID,
			values,
		)
	}
	switch status := VariableGroupErrorStatus(err); status {
	case http.StatusSeeOther:
		if !isFragmentRequest(r) {
			redirectToVariableList(w, r, projectID)
			return
		}
		h.renderVariableGroup(w, r, projectID, anchorID, http.StatusOK, "")
	case http.StatusNotFound:
		http.NotFound(w, r)
	case http.StatusInternalServerError:
		http.Error(w, "Could not save variable values", status)
	case http.StatusConflict:
		h.renderVariableGroup(
			w,
			r,
			projectID,
			anchorID,
			status,
			"The variable group changed. Review the current values and try again.",
		)
	default:
		h.renderVariableGroup(w, r, projectID, anchorID, status, err.Error())
	}
}

func redirectToVariableList(w http.ResponseWriter, r *http.Request, id int64) {
	http.Redirect(w, r, fmt.Sprintf("/projects/%d/variables", id),
		http.StatusSeeOther)
}

func parseVariableGroupPath(
	w http.ResponseWriter, r *http.Request,
) (int64, int64, bool) {
	projectID, projectErr := parseProjectID(r)
	anchorID, err := strconv.ParseInt(chi.URLParam(r, "varId"), 10, 64)
	if projectErr != nil || err != nil || anchorID <= 0 {
		http.Error(w, "Invalid variable group", http.StatusBadRequest)
		return 0, 0, false
	}
	return projectID, anchorID, true
}

func parseVariableGroupValues(
	form url.Values,
) ([]repository.VariableGroupValue, error) {
	result := make([]repository.VariableGroupValue, 0, len(form["override_id"]))
	for _, key := range form["override_id"] {
		id, idErr := strconv.ParseInt(key, 10, 64)
		revision, revisionErr := strconv.ParseInt(
			form.Get("revision_"+key),
			10,
			64,
		)
		value := form["value_"+key]
		if idErr != nil || id <= 0 || revisionErr != nil || revision < 0 ||
			len(form["revision_"+key]) != 1 || len(value) != 1 {
			return nil, repository.ErrVariableGroupValues
		}
		result = append(result, repository.VariableGroupValue{
			ID: id, Value: value[0], Revision: &revision,
		})
	}
	return result, nil
}

func VariableGroupErrorStatus(err error) int {
	switch {
	case err == nil:
		return http.StatusSeeOther
	case errors.Is(err, sql.ErrNoRows):
		return http.StatusNotFound
	case errors.Is(err, repository.ErrVariableGroupValues):
		return http.StatusBadRequest
	case errors.Is(err, repository.ErrVariableGroupChanged):
		return http.StatusConflict
	default:
		if status := ArtifactErrorStatus(err); status != 0 {
			return status
		}
		return http.StatusInternalServerError
	}
}

func (h *VariableHandler) renderVariableGroup(
	w http.ResponseWriter, r *http.Request, projectID, anchorID int64,
	status int, message string,
) {
	if !isFragmentRequest(r) {
		http.Error(w, message, status)
		return
	}
	ctx := r.Context()
	project, err := h.repo.Queries.GetProject(ctx, projectID)
	if err != nil {
		http.Error(w, "Could not load project", VariableGroupErrorStatus(err))
		return
	}
	rows, err := h.repo.VariableGroupForDisplay(ctx, projectID, anchorID)
	if err != nil {
		http.Error(
			w,
			"Could not load variable group",
			VariableGroupErrorStatus(err),
		)
		return
	}
	all, options, err := h.repo.VariableEnvironments(ctx, project)
	if err != nil {
		http.Error(
			w,
			"Could not load environments",
			http.StatusInternalServerError,
		)
		return
	}
	environments := pages.VariableEnvironments{All: all, Options: options}
	query := r.URL.Query()
	layout := "desktop"
	if query.Get("layout") == "mobile" {
		layout = "mobile"
	}
	switch {
	case r.Method == http.MethodPost && status == http.StatusOK:
		layout = ""
		SetToastSuccess(w, "Variable values saved")
	case r.Method == http.MethodGet && query.Get("cancel") == "1":
		layout = ""
	case status >= http.StatusBadRequest:
		w.Header().Set(hxRetargetHeader, "closest [data-variable-group]")
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set(hxReswapHeader, "none")
	w.WriteHeader(status)
	err = pages.VariableGroupFragment(
		projectID, anchorID, rows, environments, layout, message,
	).Render(ctx, w)
	if err != nil {
		http.Error(
			w,
			"Could not render variable group",
			http.StatusInternalServerError,
		)
	}
}
