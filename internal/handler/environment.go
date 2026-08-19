package handler

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/views/pages"
)

type EnvironmentHandler struct {
	Repo *repository.Repository
}

func NewEnvironmentHandler(repo *repository.Repository) *EnvironmentHandler {
	return &EnvironmentHandler{Repo: repo}
}

func (h *EnvironmentHandler) ListEnvironments(
	w http.ResponseWriter,
	r *http.Request,
) {
	envs, err := h.Repo.Queries.ListEnvironments(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	pages.EnvironmentsList(envs, r.URL.Path).Render(r.Context(), w)
}

func (h *EnvironmentHandler) NewEnvironment(
	w http.ResponseWriter,
	r *http.Request,
) {
	data, err := h.environmentFormData(r.Context(), &db.Environment{})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		pages.EnvironmentFormFragment(data, true, "").
			Render(r.Context(), w)
	} else {
		pages.EnvironmentForm(data, true, "", r.URL.Path).
			Render(r.Context(), w)
	}
}

func (h *EnvironmentHandler) CreateEnvironment(
	w http.ResponseWriter,
	r *http.Request,
) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	environment := &db.Environment{
		Name: strings.TrimSpace(r.FormValue("name")),
		Description: sql.NullString{
			String: r.FormValue("description"),
			Valid:  r.FormValue("description") != "",
		},
		Tags: sql.NullString{
			String: r.FormValue("tags"),
			Valid:  r.FormValue("tags") != "",
		},
	}
	data, err := h.environmentFormData(r.Context(), environment)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	policy, policyErr := parseEnvironmentPolicy(r)
	data.Policy = policy
	if environment.Name == "" {
		writeEnvironmentFormError(w, r, environmentFormErrorResponse{
			Data: data, IsNew: true, Message: "Name is required",
		})
		return
	}
	if policyErr != nil {
		writeEnvironmentFormError(w, r, environmentFormErrorResponse{
			Data: data, IsNew: true, Message: policyErr.Error(),
		})
		return
	}

	err = h.createEnvironmentWithPolicy(r.Context(), db.CreateEnvironmentParams{
		Name: environment.Name, Description: environment.Description, Tags: environment.Tags,
	}, policy)
	if err != nil {
		if IsUniqueViolation(err) {
			writeEnvironmentFormError(w, r, environmentFormErrorResponse{
				Data: data, IsNew: true,
				Message: "An environment with this name already exists",
			})
			return
		}
		if isEnvironmentPolicyInputError(err) {
			writeEnvironmentFormError(w, r, environmentFormErrorResponse{
				Data: data, IsNew: true, Message: err.Error(),
			})
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		envs, err := h.Repo.Queries.ListEnvironments(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		pages.EnvironmentsListContent(envs).Render(r.Context(), w)
	} else {
		http.Redirect(w, r, "/environments", http.StatusSeeOther)
	}
}

func (h *EnvironmentHandler) EditEnvironment(
	w http.ResponseWriter,
	r *http.Request,
) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	env, err := h.Repo.Queries.GetEnvironment(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data, err := h.environmentFormData(r.Context(), &env)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		pages.EnvironmentFormFragment(data, false, "").Render(r.Context(), w)
	} else {
		pages.EnvironmentForm(data, false, "", r.URL.Path).
			Render(r.Context(), w)
	}
}

func (h *EnvironmentHandler) UpdateEnvironment(
	w http.ResponseWriter,
	r *http.Request,
) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	environment := &db.Environment{
		ID: id, Name: strings.TrimSpace(r.FormValue("name")),
		Description: sql.NullString{
			String: r.FormValue("description"),
			Valid:  r.FormValue("description") != "",
		},
		Tags: sql.NullString{
			String: r.FormValue("tags"),
			Valid:  r.FormValue("tags") != "",
		},
	}
	data, err := h.environmentFormData(r.Context(), environment)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	policy, policyErr := parseEnvironmentPolicy(r)
	data.Policy = policy
	if environment.Name == "" {
		writeEnvironmentFormError(w, r, environmentFormErrorResponse{
			Data: data, Message: "Name is required",
		})
		return
	}
	if policyErr != nil {
		writeEnvironmentFormError(w, r, environmentFormErrorResponse{
			Data: data, Message: policyErr.Error(),
		})
		return
	}

	err = h.updateEnvironmentWithPolicy(r.Context(), db.UpdateEnvironmentParams{
		ID: environment.ID, Name: environment.Name,
		Description: environment.Description, Tags: environment.Tags,
	}, policy)
	if err != nil {
		if IsUniqueViolation(err) {
			writeEnvironmentFormError(w, r, environmentFormErrorResponse{
				Data: data, Message: "An environment with this name already exists",
			})
			return
		}
		if isEnvironmentPolicyInputError(err) {
			writeEnvironmentFormError(w, r, environmentFormErrorResponse{
				Data: data, Message: err.Error(),
			})
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		envs, err := h.Repo.Queries.ListEnvironments(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		pages.EnvironmentsListContent(envs).Render(r.Context(), w)
	} else {
		http.Redirect(w, r, "/environments", http.StatusSeeOther)
	}
}

func (h *EnvironmentHandler) DeleteEnvironment(
	w http.ResponseWriter,
	r *http.Request,
) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	if err := h.Repo.Queries.DeleteEnvironment(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	envs, err := h.Repo.Queries.ListEnvironments(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	pages.EnvironmentsListContent(envs).Render(r.Context(), w)
}
