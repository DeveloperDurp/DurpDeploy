package handler

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/views/pages"
)

type ArtifactHandler struct{ repo *repository.Repository }

func NewArtifactHandler(
	repo *repository.Repository,
) *ArtifactHandler {
	return &ArtifactHandler{repo}
}

func (h *ArtifactHandler) projectSource(
	w http.ResponseWriter,
	r *http.Request,
) (db.Project, db.PackageRepository, bool) {
	id, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project", 400)
		return db.Project{}, db.PackageRepository{}, false
	}
	project, err := h.repo.Queries.GetProject(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return project, db.PackageRepository{}, false
	}
	source, err := h.repo.Queries.GetProjectArtifactRepository(r.Context(), id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "Cannot read package repository", 500)
		return project, source, false
	}
	if source.ID == 0 {
		source.ProjectID = id
		source.AuthType = "noauth"
	}
	return project, source, true
}

func (h *ArtifactHandler) Get(w http.ResponseWriter, r *http.Request) {
	project, source, ok := h.projectSource(w, r)
	if !ok {
		return
	}
	if err := pages.ArtifactRepositoryPage(project, source, artifact.Pin{}, "", r.URL.Path).
		Render(r.Context(), w); err != nil {
		http.Error(w, "Cannot render package repository", 500)
	}
}

func (h *ArtifactHandler) Form(w http.ResponseWriter, r *http.Request) {
	project, source, ok := h.projectSource(w, r)
	if !ok {
		return
	}
	if err := pages.ArtifactRepositoryForm(project, source, "", r.URL.Path).
		Render(r.Context(), w); err != nil {
		http.Error(w, "Cannot render repository", 500)
	}
}

func (h *ArtifactHandler) Save(w http.ResponseWriter, r *http.Request) {
	project, _, ok := h.projectSource(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", 400)
		return
	}
	source := artifact.Repository{
		URLTemplate: r.FormValue("url_template"),
		AuthType:    r.FormValue("auth_type"),
		Username:    r.FormValue("username"),
		Credential:  r.FormValue("credential"),
	}
	_, err := h.repo.SaveProjectPackageRepository(
		r.Context(),
		project.ID,
		source,
	)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		row := db.PackageRepository{
			ProjectID:   project.ID,
			UrlTemplate: source.URLTemplate,
			AuthType:    source.AuthType,
			Username:    source.Username,
		}
		w.WriteHeader(422)
		if err := pages.ArtifactRepositoryForm(project, row, "Cannot save repository. Check the HTTPS template and authentication. Enter new credentials when replacing the source.", r.URL.Path).
			Render(r.Context(), w); err != nil {
			http.Error(w, "Cannot render repository", 500)
		}
		return
	}
	http.Redirect(
		w,
		r,
		fmt.Sprintf("/projects/%d/package-repository", project.ID),
		303,
	)
}

func (h *ArtifactHandler) Delete(w http.ResponseWriter, r *http.Request) {
	project, _, ok := h.projectSource(w, r)
	if !ok {
		return
	}
	if err := h.repo.RemoveProjectPackageRepository(
		r.Context(),
		project.ID,
	); err != nil {
		http.Error(w, "Cannot remove package repository", 500)
		return
	}
	http.Redirect(
		w,
		r,
		fmt.Sprintf("/projects/%d/package-repository", project.ID),
		303,
	)
}

func (h *ArtifactHandler) Test(w http.ResponseWriter, r *http.Request) {
	project, source, ok := h.projectSource(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", 400)
		return
	}
	version := r.FormValue("version")
	pin, err := h.repo.TestProjectPackage(r.Context(), project.ID, version)
	message := ""
	if err != nil {
		message = "Could not retrieve and validate this ZIP. Check the version, source URL, credentials, and network access."
	}
	if r.Header.Get("HX-Request") == "true" {
		if err := pages.PackageTestResult(pin, message).
			Render(r.Context(), w); err != nil {
			http.Error(w, "Cannot render package test", 500)
		}
		return
	}
	if err := pages.ArtifactRepositoryPage(project, source, pin, message, fmt.Sprintf("/projects/%d/package-repository", project.ID)).
		Render(r.Context(), w); err != nil {
		http.Error(w, "Cannot render package test", 500)
	}
}
