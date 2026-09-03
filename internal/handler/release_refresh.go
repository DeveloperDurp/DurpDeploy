package handler

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/db"
)

func (h *ReleaseHandler) RefreshRelease(
	w http.ResponseWriter,
	r *http.Request,
) {
	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project ID", http.StatusBadRequest)
		return
	}

	releaseIDStr := chi.URLParam(r, "releaseId")
	releaseID, err := strconv.ParseInt(releaseIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid release ID", http.StatusBadRequest)
		return
	}

	release, err := h.repo.Queries.GetRelease(r.Context(), releaseID)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Release not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if release.ProjectID != projectID {
		http.Error(w, "Release not found", http.StatusNotFound)
		return
	}

	steps, err := h.repo.Queries.ListStepsByProject(r.Context(), projectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	stepsJSON, err := BuildReleaseStepsJSON(steps)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	tx, err := h.repo.DB.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	qtx := h.repo.Queries.WithTx(tx)

	if _, err := qtx.UpdateRelease(r.Context(), db.UpdateReleaseParams{
		ID:        releaseID,
		ProjectID: projectID,
		Version:   release.Version,
		StepsJson: stepsJSON,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := qtx.DeleteReleaseVariablesByRelease(
		r.Context(),
		releaseID,
	); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	variables, err := h.repo.ListVariablesByProject(
		r.Context(),
		projectID,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	for _, v := range variables {
		// v.Value was decrypted by ListVariablesByProject above;
		// re-encrypt it before writing the release snapshot (P1-3).
		encValue, err := h.repo.EncryptValue(v.Value)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if _, err := qtx.CreateReleaseVariable(
			r.Context(),
			db.CreateReleaseVariableParams{
				ReleaseID:     releaseID,
				Name:          v.Name,
				Value:         encValue,
				EnvironmentID: v.EnvironmentID,
				Secret:        v.Secret,
			},
		); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	if err := tx.Commit(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(
		w,
		r,
		fmt.Sprintf("/projects/%d/releases/%d", projectID, releaseID),
		http.StatusSeeOther,
	)
}
