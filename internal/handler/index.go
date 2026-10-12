package handler

import (
	"net/http"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/views/pages"
)

type IndexHandler struct{ repo *repository.Repository }

func NewIndexHandler(
	repo *repository.Repository,
) *IndexHandler {
	return &IndexHandler{repo: repo}
}

func (h *IndexHandler) Index(w http.ResponseWriter, r *http.Request) {
	admin, userID := dashboardScope(r)
	projects, err := h.repo.Queries.CountDashboardProjects(
		r.Context(),
		db.CountDashboardProjectsParams{IsAdmin: admin, UserID: userID},
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	envs, err := h.repo.Queries.CountEnvironments(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data, err := h.deployments(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := pages.IndexPage(r.URL.Path, int(projects), int(envs), data).
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

func dashboardScope(r *http.Request) (int64, int64) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		return 0, 0
	}
	if user.Role == "admin" {
		return 1, user.ID
	}
	return 0, user.ID
}

func (h *IndexHandler) dashboardRows(
	r *http.Request,
	section string,
) ([]pages.DeploymentListItem, error) {
	admin, userID := dashboardScope(r)
	limit := int64(2147483647)
	if section == "recent" {
		limit = 5
	}
	rows, err := h.repo.Queries.ListDashboardDeployments(
		r.Context(),
		db.ListDashboardDeploymentsParams{
			IsAdmin:  admin,
			UserID:   userID,
			Section:  section,
			RowLimit: limit,
		},
	)
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
			ProjectName:     row.ProjectName,
			ReleaseVersion:  row.ReleaseVersion,
			EnvironmentName: row.EnvironmentName,
		}
	}
	return items, nil
}

func (h *IndexHandler) deployments(
	r *http.Request,
) (pages.HomeDeploymentsView, error) {
	var data pages.HomeDeploymentsView
	var err error
	admin, userID := dashboardScope(r)
	data.Today, err = h.repo.Queries.CountDashboardDeploymentsToday(
		r.Context(),
		db.CountDashboardDeploymentsTodayParams{IsAdmin: admin, UserID: userID},
	)
	if err != nil {
		return data, err
	}
	data.Recent, err = h.dashboardRows(r, "recent")
	if err != nil {
		return data, err
	}
	active, err := h.dashboardRows(r, "active")
	if err != nil {
		return data, err
	}
	for _, item := range active {
		switch item.Deployment.Status {
		case "queued", "pending_approval", "awaiting_artifact_approval":
			data.Waiting = append(data.Waiting, item)
		default:
			data.Running = append(data.Running, item)
		}
	}
	data.Latest, err = h.dashboardRows(r, "latest")
	return data, err
}
