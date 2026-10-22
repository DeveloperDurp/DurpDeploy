package api

import (
	"errors"
	"net/http"

	"durpdeploy/internal/audit"
	"durpdeploy/internal/auth"
	"durpdeploy/internal/repository"
)

func creationIdempotency(w http.ResponseWriter, r *http.Request,
	projectID int64, operation string, body any,
) (repository.IdempotencyRequest, bool) {
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) == 0 {
		return repository.IdempotencyRequest{}, true
	}
	if len(keys) != 1 || len(keys[0]) == 0 || len(keys[0]) > 255 {
		RespondError(w, http.StatusBadRequest,
			"Idempotency-Key must contain 1 to 255 visible ASCII characters")
		return repository.IdempotencyRequest{}, false
	}
	for _, c := range keys[0] {
		if c < 33 || c > 126 {
			RespondError(
				w,
				http.StatusBadRequest,
				"Idempotency-Key must contain 1 to 255 visible ASCII characters",
			)
			return repository.IdempotencyRequest{}, false
		}
	}
	user := auth.UserFromContext(r.Context())
	if user == nil {
		RespondError(w, http.StatusUnauthorized, "Unauthorized")
		return repository.IdempotencyRequest{}, false
	}
	request, err := repository.NewIdempotencyRequest(
		user.ID, projectID, operation, keys[0], body)
	if err != nil {
		RespondError(w, http.StatusInternalServerError,
			"Unable to prepare idempotent request")
		return repository.IdempotencyRequest{}, false
	}
	return request, true
}

// Call after authentication and project authorization, including after a
// failed claim has rolled back. Never run deployment side effects on replay.
func replayCreation(w http.ResponseWriter, r *http.Request,
	repo *repository.Repository, request repository.IdempotencyRequest,
) bool {
	body, err := repo.LookupIdempotency(r.Context(), request)
	if errors.Is(err, repository.ErrIdempotencyConflict) {
		RespondError(w, http.StatusConflict, err.Error())
		return true
	}
	if err != nil {
		RespondError(w, http.StatusInternalServerError,
			"Unable to read idempotent result")
		return true
	}
	if body == nil {
		return false
	}
	audit.Suppress(r)
	w.Header().Set("Idempotency-Replayed", "true")
	RespondJSON(w, http.StatusCreated, body)
	return true
}
