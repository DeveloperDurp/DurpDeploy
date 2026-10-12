package api

import (
	"net/http"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
)

// DeploymentActivityDay contains counts by current status for one UTC date.
// swagger:model DeploymentActivityDay
type deploymentActivityDay struct {
	Date   string           `json:"date"`
	Counts map[string]int64 `json:"counts"`
}

// DeploymentActivity returns 14 UTC calendar days, including today.
// Counts include only deployments in projects the caller can access.
//
// swagger:route GET /deployments/activity deployments deploymentActivity
//
// Deployment counts for the last 14 UTC calendar days, including today.
//
// Security:
//
//	bearer:
//
// Responses:
// 200: body:[]DeploymentActivityDay
// 401: body:UnauthorizedError
// 500: body:ServerError
func (h *DeploymentHandler) DeploymentActivity(
	w http.ResponseWriter,
	r *http.Request,
) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		RespondError(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	var isAdmin int64
	if user.Role == "admin" {
		isAdmin = 1
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	start := today.AddDate(0, 0, -13)
	rows, err := h.repo.Queries.DeploymentActivity(
		r.Context(), db.DeploymentActivityParams{
			FromUnix: start.Unix(),
			ToUnix:   today.AddDate(0, 0, 1).Unix(),
			IsAdmin:  isAdmin,
			UserID:   user.ID,
		},
	)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	days := make([]deploymentActivityDay, 14)
	for i := range days {
		days[i] = deploymentActivityDay{
			Date:   start.AddDate(0, 0, i).Format(time.DateOnly),
			Counts: make(map[string]int64),
		}
	}
	for _, row := range rows {
		index := row.Day - start.Unix()/86400
		days[index].Counts[row.Status] = row.Count
	}
	w.Header().Set("Cache-Control", "no-store")
	RespondJSON(w, http.StatusOK, days)
}
