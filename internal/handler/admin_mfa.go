package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/audit"
	"durpdeploy/internal/auth"
)

func (h *AuthHandler) AdminMFAResetPost(
	w http.ResponseWriter,
	r *http.Request,
) {
	caller, session, ok := h.securityIdentity(r)
	if !ok || caller.Role != "admin" {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid reset request", http.StatusUnprocessableEntity)
		return
	}
	targetID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || targetID <= 0 {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}
	if caller.ID == targetID {
		http.Error(w, "Cannot reset your own MFA", http.StatusForbidden)
		return
	}
	reason, ok := mfaResetReason(r.PostFormValue("reason"))
	if !ok {
		http.Error(w, "Invalid reset reason", http.StatusUnprocessableEntity)
		return
	}
	if _, err := h.repo.Queries.GetUserByID(r.Context(), targetID); err != nil {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}
	if !auth.IsRecentlyAuthenticated(session) {
		intent, err := json.Marshal(adminMFAResetIntent{
			TargetUserID: targetID,
			Reason:       reason,
		})
		if err != nil {
			http.Error(w, "Unable to reset MFA", http.StatusUnprocessableEntity)
			return
		}
		if err := h.challengeService().ReplaceAdminMFAResetContinuation(
			r.Context(),
			caller.ID,
			session.ID,
			string(intent),
		); err != nil {
			http.Error(w, "Unable to reset MFA", http.StatusUnprocessableEntity)
			return
		}
		audit.Suppress(r)
		http.Redirect(w, r, "/settings/security/reauth", http.StatusSeeOther)
		return
	}
	factors, err := h.mfaFactors()
	if err != nil {
		http.Error(w, "Unable to reset MFA", http.StatusUnprocessableEntity)
		return
	}
	if err := factors.Reset(r.Context(), targetID); err != nil {
		http.Error(w, "Unable to reset MFA", http.StatusUnprocessableEntity)
		return
	}
	r.PostForm.Set("reason", reason)
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

func mfaResetReason(value string) (string, bool) {
	switch strings.TrimSpace(value) {
	case "", "administrative_reset":
		return "administrative_reset", true
	case "lost_device", "security_incident":
		return strings.TrimSpace(value), true
	default:
		return "", false
	}
}
