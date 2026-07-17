package handler

import (
	"net/http"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/views/pages"
)

type IndexHandler struct {
	repo *repository.Repository
}

func NewIndexHandler(repo *repository.Repository) *IndexHandler {
	return &IndexHandler{repo: repo}
}

func (h *IndexHandler) Index(w http.ResponseWriter, r *http.Request) {
	projects, err := h.repo.Queries.ListProjects(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	envs, err := h.repo.Queries.ListEnvironments(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	deployments, err := h.repo.Queries.ListRecentDeployments(r.Context(), 5)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	items := make([]pages.DeploymentListItem, len(deployments))
	for i, d := range deployments {
		release, err := h.repo.Queries.GetRelease(r.Context(), d.ReleaseID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		project, err := h.repo.Queries.GetProject(
			r.Context(),
			release.ProjectID,
		)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		env, err := h.repo.Queries.GetEnvironment(r.Context(), d.EnvironmentID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		items[i] = pages.DeploymentListItem{
			Deployment:      d,
			ProjectName:     project.Name,
			ReleaseVersion:  release.Version,
			EnvironmentName: env.Name,
		}
	}

	deploymentsToday, err := h.repo.Queries.CountDeploymentsToday(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	runningRows, err := h.repo.Queries.ListRunningDeploymentsWithRefs(
		r.Context(),
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	runningDeployments := make([]pages.DeploymentListItem, len(runningRows))
	for i, row := range runningRows {
		runningDeployments[i] = pages.DeploymentListItem{
			Deployment: db.Deployment{
				ID:            row.ID,
				ReleaseID:     row.ReleaseID,
				EnvironmentID: row.EnvironmentID,
				Status:        row.Status,
				StartedAt:     row.StartedAt,
				FinishedAt:    row.FinishedAt,
				CreatedAt:     row.CreatedAt,
				Forced:        row.Forced,
				Note:          row.Note,
			},
			ProjectName:     row.ProjectName,
			ReleaseVersion:  row.ReleaseVersion,
			EnvironmentName: row.EnvironmentName,
		}
	}

	latestRows, err := h.repo.Queries.ListLatestDeploymentPerReleaseEnv(
		r.Context(),
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	latestPerReleaseEnv := make([]pages.DeploymentListItem, len(latestRows))
	for i, row := range latestRows {
		latestPerReleaseEnv[i] = pages.DeploymentListItem{
			Deployment: db.Deployment{
				ID:            row.ID,
				ReleaseID:     row.ReleaseID,
				EnvironmentID: row.EnvironmentID,
				Status:        row.Status,
				StartedAt:     row.StartedAt,
				FinishedAt:    row.FinishedAt,
				CreatedAt:     row.CreatedAt,
				Forced:        row.Forced,
				Note:          row.Note,
			},
			ProjectName:     row.ProjectName,
			ReleaseVersion:  row.ReleaseVersion,
			EnvironmentName: row.EnvironmentName,
		}
	}

	if err := pages.IndexPage(
		r.URL.Path,
		len(projects),
		len(envs),
		deploymentsToday,
		items,
		runningDeployments,
		latestPerReleaseEnv,
	).Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
