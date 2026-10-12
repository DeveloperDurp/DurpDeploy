package handler

import (
	"net/http"

	"durpdeploy/views/pages"
)

func (h *ReleaseHandler) renderReleaseCreateError(
	w http.ResponseWriter,
	r *http.Request,
	state pages.ReleaseFormState,
) {
	project, err := h.repo.Queries.GetProject(r.Context(), state.ProjectID)
	if err != nil {
		http.Error(w, "Cannot read project", http.StatusInternalServerError)
		return
	}
	releases, err := h.repo.Queries.ListReleasesByProject(
		r.Context(), state.ProjectID,
	)
	if err != nil {
		http.Error(w, "Cannot read releases", http.StatusInternalServerError)
		return
	}
	views, err := buildReleaseViews(r.Context(), h.repo, releases)
	if err != nil {
		http.Error(
			w,
			"Cannot read release activity",
			http.StatusInternalServerError,
		)
		return
	}
	w.Header().Set("HX-Retarget", "#releases-content")
	w.Header().Set("HX-Reswap", "outerHTML")
	WriteFormError(w, r,
		pages.ReleaseCreateErrorFragment(project, views, state),
		pages.ReleaseCreateErrorPage(project, views, state))
}
