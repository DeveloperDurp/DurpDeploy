package handler

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/views/pages"
	"github.com/go-chi/chi/v5"
)

type ArtifactHandler struct{ repo *repository.Repository }

func NewArtifactHandler(
	repo *repository.Repository,
) *ArtifactHandler {
	return &ArtifactHandler{repo}
}

func (h *ArtifactHandler) List(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project", 400)
		return
	}
	project, err := h.repo.Queries.GetProject(r.Context(), projectID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	rows, err := h.repo.Queries.ListPackageRepositories(r.Context(), projectID)
	if err != nil {
		http.Error(w, "Cannot list repositories", 500)
		return
	}
	selected, err := h.repo.Queries.GetProjectArtifactRepository(
		r.Context(),
		projectID,
	)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "Cannot read selection", 500)
		return
	}
	if err := pages.ArtifactRepositoriesPage(project, rows, selected.ID, r.URL.Path).
		Render(r.Context(), w); err != nil {
		http.Error(w, "Cannot render repositories", 500)
	}
}

func (h *ArtifactHandler) Form(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project", 400)
		return
	}
	project, err := h.repo.Queries.GetProject(r.Context(), projectID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	row := db.PackageRepository{ProjectID: projectID, AuthType: "noauth"}
	if raw := chi.URLParam(r, "repositoryId"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			http.Error(w, "Invalid repository", 400)
			return
		}
		row, err = h.repo.Queries.GetPackageRepository(r.Context(), id)
		if err != nil || row.ProjectID != projectID {
			http.NotFound(w, r)
			return
		}
	}
	if err := pages.ArtifactRepositoryForm(project, row, "", r.URL.Path).
		Render(r.Context(), w); err != nil {
		http.Error(w, "Cannot render repository", 500)
	}
}

func (h *ArtifactHandler) Save(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project", 400)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", 400)
		return
	}
	row := db.PackageRepository{
		ProjectID:   projectID,
		Name:        r.FormValue("name"),
		UrlTemplate: r.FormValue("url_template"),
		AuthType:    r.FormValue("auth_type"),
		Username:    r.FormValue("username"),
	}
	if raw := chi.URLParam(r, "repositoryId"); raw != "" {
		row.ID, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || row.ID <= 0 {
			http.Error(w, "Invalid repository", 400)
			return
		}
		_, err = h.repo.UpdatePackageRepository(
			r.Context(),
			db.UpdatePackageRepositoryParams{
				ID:          row.ID,
				ProjectID:   projectID,
				Name:        row.Name,
				UrlTemplate: row.UrlTemplate,
				AuthType:    row.AuthType,
				Username:    row.Username,
				Credential:  r.FormValue("credential"),
			},
		)
	} else {
		_, err = h.repo.CreatePackageRepository(
			r.Context(),
			db.CreatePackageRepositoryParams{
				ProjectID:   projectID,
				Name:        row.Name,
				UrlTemplate: row.UrlTemplate,
				AuthType:    row.AuthType,
				Username:    row.Username,
				Credential:  r.FormValue("credential"),
			},
		)
	}
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		project, loadErr := h.repo.Queries.GetProject(r.Context(), projectID)
		if loadErr != nil {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(422)
		if err := pages.ArtifactRepositoryForm(project, row, "Cannot save repository. Check the HTTPS template and authentication. Pinned repositories only allow name or credential changes.", r.URL.Path).
			Render(r.Context(), w); err != nil {
			http.Error(w, "Cannot render repository", 500)
		}
		return
	}
	http.Redirect(
		w,
		r,
		fmt.Sprintf("/projects/%d/package-repositories", projectID),
		303,
	)
}

func (h *ArtifactHandler) Select(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project", 400)
		return
	}
	id, err := strconv.ParseInt(r.FormValue("repository_id"), 10, 64)
	if err != nil || id < 0 {
		http.Error(w, "Invalid repository", 422)
		return
	}
	if err := h.repo.SelectArtifactRepository(
		r.Context(),
		db.SelectProjectArtifactRepositoryParams{
			ProjectID:    projectID,
			RepositoryID: id,
		},
	); err != nil {
		http.Error(w, "Cannot select repository", 422)
		return
	}
	http.Redirect(
		w,
		r,
		fmt.Sprintf("/projects/%d/package-repositories", projectID),
		303,
	)
}

func (h *ArtifactHandler) Delete(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project", 400)
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "repositoryId"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "Invalid repository", 400)
		return
	}
	if err := h.repo.DeletePackageRepository(
		r.Context(),
		db.DeletePackageRepositoryParams{ProjectID: projectID, ID: id},
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(
			w,
			"Cannot delete a repository referenced by an artifact pin",
			409,
		)
		return
	}
	http.Redirect(
		w,
		r,
		fmt.Sprintf("/projects/%d/package-repositories", projectID),
		303,
	)
}
