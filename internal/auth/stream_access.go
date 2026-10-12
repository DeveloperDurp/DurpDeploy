package auth

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

// CanStreamDeployment rechecks durable credentials, roles and membership.
// Callers stop on false and never expose authorization failures as log data.
func CanStreamDeployment(
	r *http.Request,
	repo *repository.Repository,
	deploymentID int64,
) bool {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	user := UserFromContext(r.Context())
	if user == nil {
		return false
	}
	userID, role := user.ID, user.Role
	if session := SessionFromContext(r.Context()); session != nil {
		current, err := repo.Queries.GetSession(
			r.Context(),
			db.GetSessionParams{ID: session.ID, ExpiresAt: time.Now().Unix()},
		)
		if err != nil || current.UserID != userID {
			return false
		}
		role = current.Role
	} else {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || !strings.HasPrefix(token, "ddp_pat_") || len(token) <= 8 {
			return false
		}
		current, err := repo.Queries.GetApiTokenByHash(
			r.Context(),
			db.GetApiTokenByHashParams{
				TokenHash: HashApiToken(token),
				ExpiresAt: sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
			},
		)
		if err != nil || current.UserID != userID {
			return false
		}
		role = current.Role
	}
	deployment, err := repo.Queries.GetDeployment(r.Context(), deploymentID)
	if err != nil {
		return false
	}
	if role == "admin" {
		return true
	}
	var projectID int64
	if deployment.Kind == "runbook" {
		execution, err := repo.Queries.GetRunbookExecutionByDeployment(
			r.Context(),
			deploymentID,
		)
		if err != nil {
			return false
		}
		projectID = execution.ProjectID
	} else {
		release, err := repo.Queries.GetRelease(
			r.Context(),
			deployment.ReleaseID,
		)
		if err != nil {
			return false
		}
		projectID = release.ProjectID
	}
	member, err := repo.Queries.IsProjectMember(
		r.Context(),
		db.IsProjectMemberParams{ProjectID: projectID, UserID: userID},
	)
	return err == nil && member == 1
}
