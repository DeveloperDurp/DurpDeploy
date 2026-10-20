package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"durpdeploy/internal/audit"
	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/views/pages"

	"github.com/go-chi/chi/v5"
)

func tokenScopeForm(r *http.Request) (repository.TokenScope, error) {
	grant := repository.TokenScope{Scope: r.FormValue("scope")}
	grant.Role = r.FormValue("role")
	if grant.Scope == "" &&
		r.FormValue("role") == "" && r.PostForm["project_ids"] == nil {
		return grant, nil
	}
	grant.ProjectIDs = []int64{}
	for _, value := range r.PostForm["project_ids"] {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id <= 0 {
			return grant, repository.ErrTokenScope
		}
		grant.ProjectIDs = append(grant.ProjectIDs, id)
	}
	return grant, nil
}

func (h *TokensHandler) tokenSelections(
	ctx context.Context, ids []string,
) (map[string][]int64, error) {
	selections := make(map[string][]int64, len(ids))
	for _, id := range ids {
		projects, err := h.repo.Queries.ListApiTokenProjectIDs(ctx, id)
		if err != nil {
			return nil, err
		}
		selections[id] = projects
	}
	return selections, nil
}

func (h *TokensHandler) renderTokenSettings(
	w http.ResponseWriter, r *http.Request,
	newToken, newName, message string, status int, missingProjects bool,
) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}
	tokens, err := h.repo.Queries.ListApiTokensByUser(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "Could not load tokens", http.StatusInternalServerError)
		return
	}
	ids := make([]string, 0, len(tokens))
	for _, token := range tokens {
		ids = append(ids, token.ID)
	}
	selections, err := h.tokenSelections(r.Context(), ids)
	if err != nil {
		http.Error(
			w,
			"Could not load token scope",
			http.StatusInternalServerError,
		)
		return
	}
	var projects []db.Project
	if user.Role == "admin" {
		projects, err = h.repo.Queries.ListProjects(r.Context())
	} else {
		projects, err = h.repo.Queries.ListProjectsForUser(r.Context(), user.ID)
	}
	if err != nil {
		http.Error(w, "Could not load projects", http.StatusInternalServerError)
		return
	}
	form, _ := tokenScopeForm(r)
	if r.Method == http.MethodGet {
		form = repository.TokenScope{Scope: "global", Role: "viewer"}
	}
	view := pages.TokenSettingsView{Projects: projects, Selections: selections,
		Form: form, Name: r.FormValue("name"),
		FormTokenID: chi.URLParam(r, "id"), MissingProjects: missingProjects}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(status)
	if err := pages.TokensSettings(tokens, newToken, newName, message,
		"/settings/tokens", view).Render(r.Context(), w); err != nil {
		http.Error(w, "Could not render tokens", http.StatusInternalServerError)
	}
}

func (h *TokensHandler) renderScopeError(
	w http.ResponseWriter, r *http.Request, err error,
) {
	missing := errors.Is(err, repository.ErrTokenProjectsRequired)
	message := "Select a valid role and accessible projects"
	if missing {
		message = "Select at least one project"
	}
	h.renderTokenSettings(w, r, "", "", message,
		http.StatusUnprocessableEntity, missing)
}

func (h *TokensHandler) MyTokensScope(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}
	id := chi.URLParam(r, "id")
	row, err := h.repo.Queries.GetApiTokenByID(r.Context(), id)
	if err != nil || row.UserID != user.ID {
		http.NotFound(w, r)
		return
	}
	request, err := tokenScopeForm(r)
	if err != nil || request.Scope == "" {
		h.renderCreateError(w, r, "Select a valid scope, role, and projects")
		return
	}
	grant, err := h.repo.ValidateTokenScope(r.Context(), *user, request)
	if err != nil {
		h.renderScopeError(w, r, err)
		return
	}
	previous, err := h.repo.ReplaceTokenScope(r.Context(), id, grant)
	if err != nil {
		http.Error(
			w,
			"Could not update token scope",
			http.StatusInternalServerError,
		)
		return
	}
	audit.SetTokenChange(r, id, grant, &previous)
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/settings/tokens")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/settings/tokens", http.StatusSeeOther)
}
