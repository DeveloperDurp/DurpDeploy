package handler

import (
	"net/http"

	"durpdeploy/internal/buildinfo"
	"durpdeploy/views/pages"
)

func Version(w http.ResponseWriter, r *http.Request) {
	if err := pages.VersionPage(buildinfo.Current(), r.URL.Path).
		Render(r.Context(), w); err != nil {
		http.Error(
			w,
			"Could not render version",
			http.StatusInternalServerError,
		)
	}
}
