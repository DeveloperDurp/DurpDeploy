package handler

import (
	"encoding/json"
	"net/http"

	"durpdeploy/internal/repository"
)

type HealthHandler struct {
	repo *repository.Repository
}

func NewHealthHandler(repo *repository.Repository) *HealthHandler {
	return &HealthHandler{repo: repo}
}

func (h *HealthHandler) Healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	err := h.repo.DB.PingContext(r.Context())
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{
			"status": "down",
			"db":     err.Error(),
		})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
		"db":     "ok",
	})
}
