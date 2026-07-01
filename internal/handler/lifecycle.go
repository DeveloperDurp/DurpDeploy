package handler

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/views/pages"
)

type LifecycleHandler struct {
	repo *repository.Repository
}

func NewLifecycleHandler(repo *repository.Repository) *LifecycleHandler {
	return &LifecycleHandler{repo: repo}
}

func (h *LifecycleHandler) ListLifecycles(w http.ResponseWriter, r *http.Request) {
	lifecycles, err := h.repo.Queries.ListLifecycles(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	rows := make([]pages.LifecycleRow, len(lifecycles))
	for i, lc := range lifecycles {
		stages, err := h.repo.Queries.ListLifecycleStages(r.Context(), lc.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		rows[i] = pages.LifecycleRow{Lifecycle: lc, StageCount: int64(len(stages))}
	}

	if err := pages.LifecyclesListPage(rows, r.URL.Path).Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *LifecycleHandler) NewLifecycle(w http.ResponseWriter, r *http.Request) {
	if err := pages.LifecycleFormPage(db.Lifecycle{}, false, "", r.URL.Path).Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *LifecycleHandler) CreateLifecycle(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		lc := db.Lifecycle{Name: name}
		WriteFormError(w, r, pages.LifecycleForm(lc, false, "Name is required"), pages.LifecycleFormPage(lc, false, "Name is required", r.URL.Path))
		return
	}

	desc := r.FormValue("description")
	_, err := h.repo.Queries.CreateLifecycle(r.Context(), db.CreateLifecycleParams{
		Name:        name,
		Description: sql.NullString{String: desc, Valid: desc != ""},
	})
	if err != nil {
		if IsUniqueViolation(err) {
			lc := db.Lifecycle{Name: name}
			WriteFormError(w, r, pages.LifecycleForm(lc, false, "A lifecycle with this name already exists"), pages.LifecycleFormPage(lc, false, "A lifecycle with this name already exists", r.URL.Path))
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/lifecycles", http.StatusSeeOther)
}

func (h *LifecycleHandler) GetLifecycle(w http.ResponseWriter, r *http.Request) {
	id, err := parseLifecycleID(r)
	if err != nil {
		http.Error(w, "Invalid lifecycle ID", http.StatusBadRequest)
		return
	}

	lc, err := h.repo.Queries.GetLifecycle(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Lifecycle not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	stages, err := h.repo.Queries.ListLifecycleStages(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	environments, err := h.repo.Queries.ListEnvironments(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	envsByID := make(map[int64]db.Environment, len(environments))
	for _, env := range environments {
		envsByID[env.ID] = env
	}

	used := make(map[int64]bool, len(stages))
	for _, s := range stages {
		used[s.EnvironmentID] = true
	}
	availableEnvs := make([]db.Environment, 0, len(environments))
	for _, env := range environments {
		if !used[env.ID] {
			availableEnvs = append(availableEnvs, env)
		}
	}

	stageViews := make([]pages.LifecycleStageView, len(stages))
	for i, s := range stages {
		stageViews[i] = pages.LifecycleStageView{
			Stage:       s,
			Environment: envsByID[s.EnvironmentID],
		}
	}

	if err := pages.LifecycleDetailPage(lc, stageViews, availableEnvs, "", r.URL.Path).Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *LifecycleHandler) EditLifecycle(w http.ResponseWriter, r *http.Request) {
	id, err := parseLifecycleID(r)
	if err != nil {
		http.Error(w, "Invalid lifecycle ID", http.StatusBadRequest)
		return
	}

	lc, err := h.repo.Queries.GetLifecycle(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Lifecycle not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := pages.LifecycleFormPage(lc, true, "", r.URL.Path).Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// SaveLifecycle handles POST /lifecycles/{id} and dispatches to update or delete
// based on the _method form field. HTML forms cannot send PUT/DELETE natively, so
// we use POST with a hidden _method override. The dispatch is per-request, so
// the route stays a single POST.
func (h *LifecycleHandler) SaveLifecycle(w http.ResponseWriter, r *http.Request) {
	id, err := parseLifecycleID(r)
	if err != nil {
		http.Error(w, "Invalid lifecycle ID", http.StatusBadRequest)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	switch r.FormValue("_method") {
	case "delete":
		if err := h.repo.Queries.DeleteLifecycle(r.Context(), id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/lifecycles", http.StatusSeeOther)
	case "put":
		name := strings.TrimSpace(r.FormValue("name"))
		if name == "" {
			lc := db.Lifecycle{ID: id, Name: name}
			WriteFormError(w, r, pages.LifecycleForm(lc, true, "Name is required"), pages.LifecycleFormPage(lc, true, "Name is required", r.URL.Path))
			return
		}
		desc := r.FormValue("description")
		_, err := h.repo.Queries.UpdateLifecycle(r.Context(), db.UpdateLifecycleParams{
			ID:          id,
			Name:        name,
			Description: sql.NullString{String: desc, Valid: desc != ""},
		})
		if err != nil {
			if IsUniqueViolation(err) {
				lc := db.Lifecycle{ID: id, Name: name}
				WriteFormError(w, r, pages.LifecycleForm(lc, true, "A lifecycle with this name already exists"), pages.LifecycleFormPage(lc, true, "A lifecycle with this name already exists", r.URL.Path))
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/lifecycles/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
	default:
		http.Error(w, "Unknown method", http.StatusBadRequest)
	}
}

func (h *LifecycleHandler) AddStage(w http.ResponseWriter, r *http.Request) {
	id, err := parseLifecycleID(r)
	if err != nil {
		http.Error(w, "Invalid lifecycle ID", http.StatusBadRequest)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	envID, err := strconv.ParseInt(r.FormValue("environment_id"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid environment ID", http.StatusBadRequest)
		return
	}

	stages, err := h.repo.Queries.ListLifecycleStages(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, s := range stages {
		if s.EnvironmentID == envID {
			http.Error(w, "Environment already in this lifecycle", http.StatusBadRequest)
			return
		}
	}

	nextOrder := int64(1)
	for _, s := range stages {
		if s.SortOrder >= nextOrder {
			nextOrder = s.SortOrder + 1
		}
	}

	if _, err := h.repo.Queries.CreateLifecycleStage(r.Context(), db.CreateLifecycleStageParams{
		LifecycleID:   id,
		EnvironmentID: envID,
		SortOrder:     nextOrder,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/lifecycles/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (h *LifecycleHandler) DeleteStage(w http.ResponseWriter, r *http.Request) {
	id, err := parseLifecycleID(r)
	if err != nil {
		http.Error(w, "Invalid lifecycle ID", http.StatusBadRequest)
		return
	}

	stageID, err := strconv.ParseInt(chi.URLParam(r, "stageId"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid stage ID", http.StatusBadRequest)
		return
	}

	if err := h.repo.Queries.DeleteLifecycleStage(r.Context(), stageID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/lifecycles/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (h *LifecycleHandler) ReorderStage(w http.ResponseWriter, r *http.Request) {
	id, err := parseLifecycleID(r)
	if err != nil {
		http.Error(w, "Invalid lifecycle ID", http.StatusBadRequest)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	stageID, err := strconv.ParseInt(r.FormValue("stage_id"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid stage ID", http.StatusBadRequest)
		return
	}
	direction := r.FormValue("direction")
	if direction != "up" && direction != "down" {
		http.Error(w, "Invalid direction", http.StatusBadRequest)
		return
	}

	stages, err := h.repo.Queries.ListLifecycleStages(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var targetIdx = -1
	for i, s := range stages {
		if s.ID == stageID {
			targetIdx = i
			break
		}
	}
	if targetIdx < 0 {
		http.Error(w, "Stage not found", http.StatusNotFound)
		return
	}

	swapIdx := targetIdx - 1
	if direction == "down" {
		swapIdx = targetIdx + 1
	}
	if swapIdx < 0 || swapIdx >= len(stages) {
		http.Redirect(w, r, "/lifecycles/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
		return
	}

	tx, err := h.repo.DB.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	qtx := h.repo.Queries.WithTx(tx)

	a := stages[targetIdx]
	b := stages[swapIdx]
	if _, err := qtx.UpdateLifecycleStage(r.Context(), db.UpdateLifecycleStageParams{
		ID:            a.ID,
		EnvironmentID: a.EnvironmentID,
		SortOrder:     b.SortOrder,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := qtx.UpdateLifecycleStage(r.Context(), db.UpdateLifecycleStageParams{
		ID:            b.ID,
		EnvironmentID: b.EnvironmentID,
		SortOrder:     a.SortOrder,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/lifecycles/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func parseLifecycleID(r *http.Request) (int64, error) {
	idStr := chi.URLParam(r, "id")
	return strconv.ParseInt(idStr, 10, 64)
}
