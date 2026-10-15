package api

import (
	"net/http"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
)

func (h *EnvironmentHandler) tokenEnvironments(r *http.Request) (
	[]db.Environment, error,
) {
	return h.repo.Queries.ListTokenEnvironments(r.Context(),
		db.ListTokenEnvironmentsParams{TokenID: auth.ScopedTokenID(r.Context()),
			UserID:  auth.UserFromContext(r.Context()).ID,
			IsAdmin: tokenOwnerAdmin(r)})
}

func (h *EnvironmentHandler) listTokenEnvironments(
	w http.ResponseWriter, r *http.Request, limit, offset int64,
) {
	rows, err := h.tokenEnvironments(r)
	if err != nil {
		RespondError(
			w,
			http.StatusInternalServerError,
			"could not list environments",
		)
		return
	}
	page := tokenPage(rows, limit, offset)
	items := make([]any, len(page))
	for i, env := range page {
		items[i] = environmentForUser(r, env)
	}
	RespondJSON(w, http.StatusOK, PaginatedResponse{Items: items,
		Total: int64(len(rows)), Limit: limit, Offset: offset})
}

func (h *EnvironmentHandler) getTokenEnvironment(
	w http.ResponseWriter, r *http.Request, id int64,
) {
	rows, err := h.tokenEnvironments(r)
	if err != nil {
		RespondError(
			w,
			http.StatusInternalServerError,
			"could not read environment",
		)
		return
	}
	for _, env := range rows {
		if env.ID == id {
			RespondJSON(w, http.StatusOK, environmentForUser(r, env))
			return
		}
	}
	RespondError(w, http.StatusNotFound, "Environment not found")
}

func (h *LifecycleHandler) tokenLifecycles(r *http.Request) (
	[]db.Lifecycle, error,
) {
	return h.repo.Queries.ListTokenLifecycles(r.Context(),
		db.ListTokenLifecyclesParams{TokenID: auth.ScopedTokenID(r.Context()),
			UserID:  auth.UserFromContext(r.Context()).ID,
			IsAdmin: tokenOwnerAdmin(r)})
}

func (h *LifecycleHandler) listTokenLifecycles(
	w http.ResponseWriter, r *http.Request, limit, offset int64,
) {
	rows, err := h.tokenLifecycles(r)
	if err != nil {
		RespondError(
			w,
			http.StatusInternalServerError,
			"could not list lifecycles",
		)
		return
	}
	page := tokenPage(rows, limit, offset)
	items := make([]any, len(page))
	for i, lc := range page {
		items[i] = lc
	}
	RespondJSON(w, http.StatusOK, PaginatedResponse{Items: items,
		Total: int64(len(rows)), Limit: limit, Offset: offset})
}

func (h *LifecycleHandler) tokenLifecycleAllowed(
	w http.ResponseWriter, r *http.Request, id int64,
) bool {
	rows, err := h.tokenLifecycles(r)
	if err != nil {
		RespondError(
			w,
			http.StatusInternalServerError,
			"could not read lifecycle",
		)
		return false
	}
	for _, lc := range rows {
		if lc.ID == id {
			return true
		}
	}
	RespondError(w, http.StatusNotFound, "Lifecycle not found")
	return false
}
