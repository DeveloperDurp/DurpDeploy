package auth

import (
	"context"
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
	userID := user.ID
	r, role, ok := loadStreamCredentials(r, repo, userID)
	if !ok {
		return false
	}
	deployment, err := repo.Queries.GetDeployment(r.Context(), deploymentID)
	if err != nil {
		return false
	}
	if role == "admin" && ScopedTokenID(r.Context()) == "" {
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
	if !TokenAllowsProject(r.Context(), projectID) {
		return false
	}
	if role == "admin" {
		return true
	}
	member, err := repo.Queries.IsProjectMember(
		r.Context(),
		db.IsProjectMemberParams{ProjectID: projectID, UserID: userID},
	)
	return err == nil && member == 1
}

func loadStreamCredentials(
	r *http.Request, repo *repository.Repository, userID int64,
) (*http.Request, string, bool) {
	if session := SessionFromContext(r.Context()); session != nil {
		current, err := repo.Queries.GetSession(
			r.Context(),
			db.GetSessionParams{ID: session.ID, ExpiresAt: time.Now().Unix()},
		)
		if err != nil || current.UserID != userID {
			return nil, "", false
		}
		return r, current.Role, true
	}
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return nil, "", false
	}
	hash := HashApiToken(token)
	if hash == "" {
		return nil, "", false
	}
	current, grant, err := loadTokenAccess(r.Context(), repo, hash)
	if err != nil || current.UserID != userID {
		return nil, "", false
	}
	r = SetUser(r, &db.User{ID: userID, Role: current.Role})
	return setTokenAccess(r, current.ID, grant), current.Role, true
}
