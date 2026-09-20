package handler

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/agentserver"
	"durpdeploy/views/pages"
)

func (h *AgentsHandler) Pair(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	if h.pairing == nil {
		h.renderListError(w, r, "Agent listener is disabled")
		return
	}
	input, err := agentserver.ParsePairingStartInput(
		r.FormValue("name"), r.FormValue("address"), r.FormValue("code"),
	)
	if err != nil {
		h.renderListError(w, r, err.Error())
		return
	}
	challenge, err := h.pairing.Begin(r.Context(), input)
	if err != nil {
		h.renderListError(w, r, pairingErrorMessage(err))
		return
	}
	http.Redirect(
		w, r, "/admin/agents/pair/"+challenge.ID, http.StatusSeeOther,
	)
}

func (h *AgentsHandler) ConfirmPair(w http.ResponseWriter, r *http.Request) {
	if h.pairing == nil {
		http.Error(w, "Agent listener is disabled", http.StatusServiceUnavailable)
		return
	}
	challenge, err := h.pairing.Challenge(chi.URLParam(r, "challengeID"))
	if err != nil {
		if errors.Is(err, agentserver.ErrPairingChallenge) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := pages.AgentPairingConfirmationPage(challenge, r.URL.Path).
		Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *AgentsHandler) ApprovePair(w http.ResponseWriter, r *http.Request) {
	if h.pairing == nil {
		http.Error(w, "Agent listener is disabled", http.StatusServiceUnavailable)
		return
	}
	result, err := h.pairing.Approve(
		r.Context(), chi.URLParam(r, "challengeID"),
	)
	if err != nil {
		http.Error(w, pairingErrorMessage(err), http.StatusUnprocessableEntity)
		return
	}
	http.Redirect(w, r, "/admin/agents/"+result.AgentID, http.StatusSeeOther)
}

func (h *AgentsHandler) DenyPair(w http.ResponseWriter, r *http.Request) {
	if h.pairing == nil {
		http.Error(w, "Agent listener is disabled", http.StatusServiceUnavailable)
		return
	}
	if err := h.pairing.Deny(chi.URLParam(r, "challengeID")); err != nil {
		if errors.Is(err, agentserver.ErrPairingChallenge) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/agents", http.StatusSeeOther)
}
