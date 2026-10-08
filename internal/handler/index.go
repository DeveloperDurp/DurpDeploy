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

	data, err := h.deployments(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := pages.IndexPage(r.URL.Path, len(projects), len(envs), data).
		Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *IndexHandler) RefreshDeployments(
	w http.ResponseWriter,
	r *http.Request,
) {
	data, err := h.deployments(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("HX-Trigger-After-Swap", "home-deployments-refreshed")
	if err := pages.HomeDeploymentsRefresh(data).
		Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *IndexHandler) deployments(
	r *http.Request,
) (pages.HomeDeploymentsView, error) {
	deployments, err := h.repo.Queries.ListRecentDeployments(r.Context(), 5)
	if err != nil {
		return pages.HomeDeploymentsView{}, err
	}

	items := make([]pages.DeploymentListItem, len(deployments))
	for i, d := range deployments {
		release, err := h.repo.Queries.GetRelease(r.Context(), d.ReleaseID)
		if err != nil {
			return pages.HomeDeploymentsView{}, err
		}
		project, err := h.repo.Queries.GetProject(
			r.Context(),
			release.ProjectID,
		)
		if err != nil {
			return pages.HomeDeploymentsView{}, err
		}
		env, err := h.repo.Queries.GetEnvironment(r.Context(), d.EnvironmentID)
		if err != nil {
			return pages.HomeDeploymentsView{}, err
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
		return pages.HomeDeploymentsView{}, err
	}

	activeDeployments, err := h.activeDeployments(r)
	if err != nil {
		return pages.HomeDeploymentsView{}, err
	}
	var runningDeployments, waitingDeployments []pages.DeploymentListItem
	for _, item := range activeDeployments {
		if item.Deployment.Status == "queued" ||
			item.Deployment.Status == "pending_approval" ||
			item.Deployment.Status == "awaiting_artifact_approval" {
			waitingDeployments = append(waitingDeployments, item)
		} else {
			runningDeployments = append(runningDeployments, item)
		}
	}

	latestRows, err := h.repo.Queries.ListLatestDeploymentPerReleaseEnv(
		r.Context(),
	)
	if err != nil {
		return pages.HomeDeploymentsView{}, err
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

	return pages.HomeDeploymentsView{
		Today:   deploymentsToday,
		Recent:  items,
		Running: runningDeployments,
		Waiting: waitingDeployments,
		Latest:  latestPerReleaseEnv,
	}, nil
}

func (h *IndexHandler) activeDeployments(
	r *http.Request,
) ([]pages.DeploymentListItem, error) {
	rows, err := h.repo.Queries.ListActiveDeploymentsWithRefs(r.Context())
	if err != nil {
		return nil, err
	}
	items := make([]pages.DeploymentListItem, len(rows))
	for i, row := range rows {
		items[i] = pages.DeploymentListItem{
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
			ProjectName: row.ProjectName, ReleaseVersion: row.ReleaseVersion,
			EnvironmentName: row.EnvironmentName,
		}
	}
	return items, nil
}
