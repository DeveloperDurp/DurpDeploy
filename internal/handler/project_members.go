package handler

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

// ProjectMembersHandler manages the per-project membership roster. The
// RequireProjectAccess middleware already admitted the request (the
// caller is a member or a global admin); these handlers enforce the
// finer-grained "per-project admin" rule for add/remove.
type ProjectMembersHandler struct {
	repo *repository.Repository
}

func NewProjectMembersHandler(
	repo *repository.Repository,
) *ProjectMembersHandler {
	return &ProjectMembersHandler{repo: repo}
}

// canManageProject returns true if the user is a global admin or a
// per-project admin of the given project. Used by both the project
// form (to gate the UI) and the members handlers (to gate writes).
func canManageProject(
	ctx context.Context,
	repo *repository.Repository,
	user *db.User,
	projectID int64,
) bool {
	if user == nil {
		return false
	}
	if user.Role == "admin" {
		return true
	}
	member, err := repo.Queries.GetProjectMember(ctx, db.GetProjectMemberParams{
		ProjectID: projectID,
		UserID:    user.ID,
	})
	if err != nil {
		return false
	}
	return member.Role == "admin"
}

// loadMembersContext loads the member roster, the users not yet on it,
// and the canManage flag for the project form. Errors are swallowed
// (rendered as empty slices / false) so a transient DB hiccup doesn't
// blank the whole edit page — the form fields are still usable.
func (h *ProjectHandler) loadMembersContext(
	r *http.Request,
	projectID int64,
) ([]db.ListProjectMembersRow, []db.ListUsersRow, bool) {
	user := auth.UserFromContext(r.Context())
	canManage := canManageProject(r.Context(), h.repo, user, projectID)

	members, err := h.repo.Queries.ListProjectMembers(r.Context(), projectID)
	if err != nil {
		members = nil
	}

	allUsers, err := h.repo.Queries.ListUsers(r.Context())
	if err != nil {
		allUsers = nil
	}

	memberIDs := make(map[int64]bool, len(members))
	for _, m := range members {
		memberIDs[m.UserID] = true
	}
	var available []db.ListUsersRow
	for _, u := range allUsers {
		if !memberIDs[u.ID] {
			available = append(available, u)
		}
	}
	return members, available, canManage
}

// ListMembers redirects to the project edit page, where the Members
// section lives. The route exists so the URL is addressable; the
// section is rendered inline on the edit page per the design decision
// ("Do NOT create a new page").
func (h *ProjectMembersHandler) ListMembers(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project ID", http.StatusBadRequest)
		return
	}
	editURL := fmt.Sprintf("/projects/%d/edit", id)
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", editURL)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, editURL, http.StatusSeeOther)
}

// AddMember adds a user to the project's member roster.
//
// ponytail: the per-project admin check is handler-level. The
// RequireProjectAccess middleware already did the binary "is a member"
// check (admitting any member); this handler enforces the finer-grained
// "is a per-project admin" rule for the write. Moving the role check
// into the middleware would make it reject per-project deployers from
// every /projects/{id}/... route, not just member management — wrong
// trade-off for now.
func (h *ProjectMembersHandler) AddMember(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project ID", http.StatusBadRequest)
		return
	}

	user := auth.UserFromContext(r.Context())
	if !canManageProject(r.Context(), h.repo, user, id) {
		auth.RenderUnauthorized(w, r)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	userID, err := strconv.ParseInt(r.FormValue("user_id"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}

	role := r.FormValue("role")
	if role != "admin" && role != "deployer" {
		http.Error(w, "Invalid role", http.StatusBadRequest)
		return
	}

	// Verify the target user exists (FK constraint would catch this,
	// but a 400 is friendlier than a 500 for a stale select option).
	if _, err := h.repo.Queries.GetUserByID(r.Context(), userID); err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "User not found", http.StatusBadRequest)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := h.repo.Queries.AddProjectMember(
		r.Context(),
		db.AddProjectMemberParams{
			ProjectID: id,
			UserID:    userID,
			Role:      role,
		},
	); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	editURL := fmt.Sprintf("/projects/%d/edit", id)
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", editURL)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, editURL, http.StatusSeeOther)
}

// RemoveMember removes a user from the project's member roster.
//
// ponytail: same handler-level per-project admin check as AddMember.
// The middleware admits any member; this handler enforces "only a
// per-project admin (or global admin) can remove members".
func (h *ProjectMembersHandler) RemoveMember(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project ID", http.StatusBadRequest)
		return
	}

	user := auth.UserFromContext(r.Context())
	if !canManageProject(r.Context(), h.repo, user, id) {
		auth.RenderUnauthorized(w, r)
		return
	}

	userIDStr := chi.URLParam(r, "userId")
	userID, err := strconv.ParseInt(userIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}

	if err := h.repo.Queries.RemoveProjectMember(
		r.Context(),
		db.RemoveProjectMemberParams{
			ProjectID: id,
			UserID:    userID,
		},
	); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	editURL := fmt.Sprintf("/projects/%d/edit", id)
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", editURL)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, editURL, http.StatusSeeOther)
}
