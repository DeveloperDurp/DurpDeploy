package handler

import (
	"database/sql"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

func (h *RunbookHandler) Schedule(w http.ResponseWriter, r *http.Request) {
	project, book, ok := h.book(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	environmentID, ok := h.parseExecutionEnvironment(w, r, project)
	if !ok {
		return
	}
	versionID, err := strconv.ParseInt(r.FormValue("version_id"), 10, 64)
	if err != nil || versionID < 0 {
		http.Error(w, "Invalid version", http.StatusUnprocessableEntity)
		return
	}
	version := sql.NullInt64{}
	if versionID != 0 {
		pinned, err := h.repo.Queries.GetRunbookVersion(
			r.Context(),
			db.GetRunbookVersionParams{
				ID:        versionID,
				RunbookID: book.ID,
			},
		)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if err := h.repo.ValidateExecutableRelease(
			r.Context(),
			pinned.ReleaseID,
		); err != nil {
			if errors.Is(err, repository.ErrLegacyServerStep) {
				http.Error(
					w,
					repository.ErrLegacyServerStep.Error(),
					http.StatusConflict,
				)
			} else {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
			return
		}
		version = sql.NullInt64{Int64: versionID, Valid: true}
	}
	parsed, err := ParseAndValidateCron(r.FormValue("cron"))
	if err != nil {
		http.Error(w, "Invalid cron", http.StatusUnprocessableEntity)
		return
	}
	if _, err := h.repo.CreateRunbookSchedule(r.Context(), project.ID,
		db.CreateRunbookScheduleParams{
			RunbookID: book.ID, VersionID: version,
			EnvironmentID: environmentID, Cron: r.FormValue("cron"),
			NextRunAt: parsed.Next(time.Now()).Unix(),
		}); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "Cannot create schedule", http.StatusInternalServerError)
		return
	}
	http.Redirect(
		w,
		r,
		fmt.Sprintf("/projects/%d/runbooks/%d", project.ID, book.ID),
		http.StatusSeeOther,
	)
}
