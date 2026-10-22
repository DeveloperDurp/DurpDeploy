package handler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/views/pages"
)

type ReleaseHandler struct {
	repo *repository.Repository
}

func NewReleaseHandler(repo *repository.Repository) *ReleaseHandler {
	return &ReleaseHandler{repo: repo}
}

func (h *ReleaseHandler) ListReleases(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project ID", http.StatusBadRequest)
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

	views, err := loadReleaseViews(r.Context(), h.repo, projectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if isFragmentRequest(r) {
		if err := pages.ReleasesFragment(project, views, "").
			Render(r.Context(), w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	} else {
		if err := pages.ReleasesPage(project, views, "", r.URL.Path).
			Render(r.Context(), w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

func loadReleaseViews(
	ctx context.Context,
	repo *repository.Repository,
	projectID int64,
) ([]pages.ReleaseView, error) {
	releases, err := repo.Queries.ListReleasesWithActivity(ctx, projectID)
	if err != nil {
		return nil, err
	}
	views := make([]pages.ReleaseView, len(releases))
	for i, rel := range releases {
		views[i] = pages.ReleaseView{
			Release: db.Release{
				ID: rel.ID, ProjectID: rel.ProjectID, Version: rel.Version,
				StepsJson: rel.StepsJson, CreatedAt: rel.CreatedAt, Kind: rel.Kind,
				SnapshotLocked: rel.SnapshotLocked,
				RepositoryUrl:  rel.RepositoryUrl, CommitSha: rel.CommitSha,
				ArtifactRef: rel.ArtifactRef, CiRunUrl: rel.CiRunUrl,
			},
			Active: rel.Active != 0,
		}
	}
	return views, nil
}

func (h *ReleaseHandler) GetRelease(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project ID", http.StatusBadRequest)
		return
	}

	releaseIDStr := chi.URLParam(r, "releaseId")
	releaseID, err := strconv.ParseInt(releaseIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid release ID", http.StatusBadRequest)
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

	release, err := h.repo.Queries.GetDeploymentRelease(r.Context(), releaseID)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Release not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Verify release belongs to project
	if release.ProjectID != projectID {
		http.Error(w, "Release not found", http.StatusNotFound)
		return
	}
	active, err := h.repo.Queries.HasActiveReleaseDeployment(
		r.Context(), releaseID,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	variables, err := h.repo.ListReleaseVariablesByRelease(
		r.Context(),
		releaseID,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	environments, err := h.repo.Queries.ListEnvironments(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	pin, err := h.repo.Queries.GetReleaseArtifact(r.Context(), releaseID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "Cannot read package pin", 500)
		return
	}
	if err := pages.ReleaseDetailPage(project, release, variables, environments, pin, active != 0, r.URL.Path).
		Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *ReleaseHandler) DeleteRelease(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project ID", http.StatusBadRequest)
		return
	}
	releaseID, err := strconv.ParseInt(chi.URLParam(r, "releaseId"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid release ID", http.StatusBadRequest)
		return
	}
	if err := h.repo.DeleteRelease(
		r.Context(), projectID, releaseID,
	); err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			http.Error(w, "Release not found", http.StatusNotFound)
		case errors.Is(err, repository.ErrReleaseHasActiveDeployment):
			http.Error(w, err.Error(), http.StatusConflict)
		default:
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	path := fmt.Sprintf("/projects/%d/releases", projectID)
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", path)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, path, http.StatusSeeOther)
}
