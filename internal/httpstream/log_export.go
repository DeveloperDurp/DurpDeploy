package httpstream

import (
	"database/sql"
	"fmt"
	"net/http"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

// ExportTextLogs shares deployment and runbook download loading and formatting.
// The caller renders errors before the download starts in its own wire format.
func ExportTextLogs(
	w http.ResponseWriter,
	r *http.Request,
	repo *repository.Repository,
	deploymentID int64,
	respondError func(int, string),
) {
	deployment, err := repo.Queries.GetDeployment(r.Context(), deploymentID)
	if err != nil {
		if err == sql.ErrNoRows {
			respondError(http.StatusNotFound, "Deployment not found")
			return
		}
		respondError(http.StatusInternalServerError, err.Error())
		return
	}
	release, err := repo.Queries.GetRelease(r.Context(), deployment.ReleaseID)
	if err != nil {
		respondError(http.StatusInternalServerError, err.Error())
		return
	}
	project, err := repo.Queries.GetProject(r.Context(), release.ProjectID)
	if err != nil {
		respondError(http.StatusInternalServerError, err.Error())
		return
	}
	environment, err := repo.Queries.GetEnvironment(
		r.Context(), deployment.EnvironmentID,
	)
	if err != nil {
		respondError(http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="deployment-%d.log"`, deploymentID))
	w.WriteHeader(http.StatusOK)
	if _, err := fmt.Fprintf(w,
		"=== deployment #%d | project=%s | release=%s | env=%s | status=%s ===\n",
		deploymentID, project.Name, release.Version,
		environment.Name, deployment.Status,
	); err != nil {
		return
	}
	err = repo.ForEachDeploymentLogByDeploymentAsc(
		r.Context(), deploymentID, func(lg db.DeploymentLog) error {
			ts := time.Unix(lg.CreatedAt, 0).UTC().Format("2006-01-02 15:04:05")
			if lg.StepName.Valid {
				_, err := fmt.Fprintf(w, "[%s] [%s] %s\n",
					ts, lg.StepName.String, lg.Line)
				return err
			}
			_, err := fmt.Fprintf(w, "[%s] %s\n", ts, lg.Line)
			return err
		},
	)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintln(w)
}
