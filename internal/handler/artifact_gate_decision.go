package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"durpdeploy/internal/audit"
	"durpdeploy/internal/repository"
	"github.com/go-chi/chi/v5"
)

// Called after authorization and route IDs and decision input are validated.
func artifactDecisionError(w http.ResponseWriter, r *http.Request, err error) {
	status, code := http.StatusInternalServerError, "error"
	if errors.Is(err, repository.ErrArtifactGate) {
		status, code = http.StatusConflict, "conflict"
	} else {
		slog.Error("Artifact decision failed",
			"deployment_id", chi.URLParam(r, "id"),
			"step_index", chi.URLParam(r, "stepIndex"), "err", err)
	}
	if strings.HasPrefix(r.URL.Path, "/api/v1/") {
		gateHTTPError(w, r, status,
			"Artifact decision failed; refresh and review the current gate")
		return
	}
	// A redirect is recovery, not a successful state change to audit.
	audit.Suppress(r)
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	http.Redirect(w, r, "/deployments/"+strconv.FormatInt(id, 10)+
		"?artifact_decision="+code, http.StatusSeeOther)
}
