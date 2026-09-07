package api

import (
	"encoding/json"
	"net/http"

	"durpdeploy/internal/deploymentstate"
	"durpdeploy/internal/repository"
)

// FanoutParentLogConflict directs callers to the immutable child log targets.
// swagger:model FanoutParentLogConflict
type FanoutParentLogConflict struct {
	Code     string                  `json:"code"`
	Message  string                  `json:"message"`
	Children []deploymentstate.Child `json:"children"`
}

func rejectParentLogs(
	w http.ResponseWriter,
	r *http.Request,
	repo *repository.Repository,
	id int64,
) bool {
	parent, err := deploymentstate.IsParent(r.Context(), repo, id)
	if err != nil {
		RespondError(
			w,
			http.StatusInternalServerError,
			"Could not load deployment routing",
		)
		return true
	}
	if !parent {
		return false
	}
	routing, err := deploymentstate.Load(r.Context(), repo, id)
	if err != nil {
		RespondError(
			w,
			http.StatusInternalServerError,
			"Could not load child deployments",
		)
		return true
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	if err := json.NewEncoder(w).Encode(FanoutParentLogConflict{Code: "fanout_parent_has_no_logs", Message: deploymentstate.ParentLogMessage, Children: routing.Children}); err != nil {
		return true
	}
	return true
}
