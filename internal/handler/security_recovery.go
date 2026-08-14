package handler

import "net/http"

func (h *AuthHandler) SecurityRecoveryContinuePost(
	w http.ResponseWriter,
	r *http.Request,
) {
	setNoStore(w)
	h.clearBrowserSessionCookie(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
