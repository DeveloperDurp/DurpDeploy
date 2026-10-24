package audit

import (
	"net/http"

	"durpdeploy/internal/repository"
)

func SetLibraryTemplateResult(
	r *http.Request,
	result repository.LibraryApplyResult,
) {
	SetProjectTemplateResult(
		r,
		"apply_library_template",
		"project",
		result.ProjectID,
	)
	if state, ok := r.Context().Value(requestStateKey{}).(*requestState); ok {
		state.libraryTemplate = &result
	}
}
