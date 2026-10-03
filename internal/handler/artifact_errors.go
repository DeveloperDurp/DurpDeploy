package handler

import (
	"errors"
	"net/http"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/containerenv"
	"durpdeploy/internal/repository"
)

func ArtifactErrorStatus(err error) int {
	switch {
	case errors.Is(err, artifact.ErrInvalid),
		errors.Is(err, containerenv.ErrReserved),
		errors.Is(err, artifact.ErrChecksum),
		errors.Is(err, repository.ErrRemoteArtifactsUnsupported),
		errors.Is(err, repository.ErrArtifactPathReserved):
		return http.StatusUnprocessableEntity
	case errors.Is(err, repository.ErrArtifactRepositoryPinned):
		return http.StatusConflict
	case errors.Is(err, artifact.ErrFetch):
		return http.StatusBadGateway
	default:
		return 0
	}
}

func writeArtifactError(w http.ResponseWriter, err error) bool {
	status := ArtifactErrorStatus(err)
	if status == 0 {
		return false
	}
	http.Error(w, err.Error(), status)
	return true
}
