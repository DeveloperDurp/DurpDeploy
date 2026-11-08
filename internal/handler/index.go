package handler

import (
	"net/http"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/views/pages"
)

// dashboardRowCap limits attention and in-flight sections. The handler
// fetches one extra row to detect overflow.
const dashboardRowCap = 15

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

// Statistics renders the volume overview: counts and the 14-day charts.
func (h *IndexHandler) Statistics(w http.ResponseWriter, r *http.Request) {
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
	today, err := h.repo.Queries.CountDashboardDeploymentsToday(
		r.Context(),
		db.CountDashboardDeploymentsTodayParams{IsAdmin: admin, UserID: userID},
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := pages.StatisticsPage(
		r.URL.Path, int(projects), int(envs), today,
	).Render(r.Context(), w); err != nil {
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
	if err := pages.HomeDeploymentSections(data).
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
) ([]pages.DeploymentListItem, bool, error) {
	admin, userID := dashboardScope(r)
	rows, err := h.repo.Queries.ListDashboardDeployments(
		r.Context(),
		db.ListDashboardDeploymentsParams{
			IsAdmin:  admin,
			UserID:   userID,
			Section:  section,
			RowLimit: dashboardRowCap + 1,
		},
	)
	if err != nil {
		return nil, false, err
	}
	capped := len(rows) > dashboardRowCap
	if capped {
		rows = rows[:dashboardRowCap]
	}
	items := make([]pages.DeploymentListItem, len(rows))
	for i, row := range rows {
		items[i] = pages.DeploymentListItem{
			Deployment: db.Deployment{
				ID:               row.ID,
				ReleaseID:        row.ReleaseID,
				EnvironmentID:    row.EnvironmentID,
				Status:           row.Status,
				StartedAt:        row.StartedAt,
				FinishedAt:       row.FinishedAt,
				CreatedAt:        row.CreatedAt,
				Forced:           row.Forced,
				Note:             row.Note,
				AnsibleCheckMode: row.AnsibleCheckMode,
			},
			ProjectName:     row.ProjectName,
			ReleaseVersion:  row.ReleaseVersion,
			EnvironmentName: row.EnvironmentName,
		}
	}
	return items, capped, nil
}

func (h *IndexHandler) deployments(
	r *http.Request,
) (pages.HomeDeploymentsView, error) {
	var data pages.HomeDeploymentsView
	var err error
	data.Attention, data.AttentionCapped, err = h.dashboardRows(r, "attention")
	if err != nil {
		return data, err
	}
	data.Running, data.RunningCapped, err = h.dashboardRows(r, "active")
	return data, err
}
