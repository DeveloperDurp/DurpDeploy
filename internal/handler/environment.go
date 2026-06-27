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

func (h *EnvironmentHandler) ListEnvironments(w http.ResponseWriter, r *http.Request) {
	envs, err := h.Repo.Queries.ListEnvironments(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	pages.EnvironmentsList(envs, r.URL.Path).Render(r.Context(), w)
}

func (h *EnvironmentHandler) NewEnvironment(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("HX-Request") == "true" {
		pages.EnvironmentFormFragment(&db.Environment{}, true, "").Render(r.Context(), w)
	} else {
		pages.EnvironmentForm(&db.Environment{}, true, "", r.URL.Path).Render(r.Context(), w)
	}
}

func (h *EnvironmentHandler) CreateEnvironment(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		WriteFormError(w, r, pages.EnvironmentFormFragment(&db.Environment{}, true, "Name is required"), pages.EnvironmentForm(&db.Environment{}, true, "Name is required", r.URL.Path))
		return
	}

	params := db.CreateEnvironmentParams{
		Name: name,
		Description: sql.NullString{
			String: r.FormValue("description"),
			Valid:  r.FormValue("description") != "",
		},
		Tags: sql.NullString{
			String: r.FormValue("tags"),
			Valid:  r.FormValue("tags") != "",
		},
	}

	_, err := h.Repo.Queries.CreateEnvironment(r.Context(), params)
	if err != nil {
		if IsUniqueViolation(err) {
			env := &db.Environment{Name: name}
			WriteFormError(w, r, pages.EnvironmentFormFragment(env, true, "An environment with this name already exists"), pages.EnvironmentForm(env, true, "An environment with this name already exists", r.URL.Path))
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

func (h *EnvironmentHandler) EditEnvironment(w http.ResponseWriter, r *http.Request) {
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

	if r.Header.Get("HX-Request") == "true" {
		pages.EnvironmentFormFragment(&env, false, "").Render(r.Context(), w)
	} else {
		pages.EnvironmentForm(&env, false, "", r.URL.Path).Render(r.Context(), w)
	}
}

func (h *EnvironmentHandler) UpdateEnvironment(w http.ResponseWriter, r *http.Request) {
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

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		env := &db.Environment{ID: id, Name: name}
		WriteFormError(w, r, pages.EnvironmentFormFragment(env, false, "Name is required"), pages.EnvironmentForm(env, false, "Name is required", r.URL.Path))
		return
	}

	params := db.UpdateEnvironmentParams{
		ID:   id,
		Name: name,
		Description: sql.NullString{
			String: r.FormValue("description"),
			Valid:  r.FormValue("description") != "",
		},
		Tags: sql.NullString{
			String: r.FormValue("tags"),
			Valid:  r.FormValue("tags") != "",
		},
	}

	_, err = h.Repo.Queries.UpdateEnvironment(r.Context(), params)
	if err != nil {
		if IsUniqueViolation(err) {
			env := &db.Environment{ID: id, Name: name}
			WriteFormError(w, r, pages.EnvironmentFormFragment(env, false, "An environment with this name already exists"), pages.EnvironmentForm(env, false, "An environment with this name already exists", r.URL.Path))
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

func (h *EnvironmentHandler) DeleteEnvironment(w http.ResponseWriter, r *http.Request) {
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
	pages.EnvironmentsListFragment(envs).Render(r.Context(), w)
}
