package handler

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/views/pages"
)

// ProjectMembersHandler manages the per-project membership roster. The
// RequireProjectAccess middleware already admitted the request (the
// caller is a member or a global admin); these handlers enforce the
// finer-grained "per-project admin" rule for member changes.
type ProjectMembersHandler struct {
	repo *repository.Repository
}

func NewProjectMembersHandler(
	repo *repository.Repository,
) *ProjectMembersHandler {
	return &ProjectMembersHandler{repo: repo}
}

// loadMembersContext loads the member roster, the users not yet on it,
// and the canManage flag for the project form. Errors are swallowed
// (rendered as empty slices / false) so a transient DB hiccup doesn't
// blank the whole edit page — the form fields are still usable.
func (h *ProjectHandler) loadMembersContext(
	r *http.Request,
	projectID int64,
) ([]db.ListProjectMembersRow, []db.ListUsersRow, bool) {
	members, available, canManage, _ := loadProjectMembersContext(
		r, h.repo, projectID,
	)
	return members, available, canManage
}

func loadProjectMembersContext(
	r *http.Request,
	repo *repository.Repository,
	projectID int64,
) ([]db.ListProjectMembersRow, []db.ListUsersRow, bool, error) {
	canManage := auth.CanManageProject(r.Context(), repo, projectID)

	members, err := repo.Queries.ListProjectMembers(r.Context(), projectID)
	if err != nil {
		return nil, nil, canManage, err
	}

	allUsers, err := repo.Queries.ListUsers(r.Context())
	if err != nil {
		return members, nil, canManage, err
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
	return members, available, canManage, nil
}

// UpdateMemberRole changes an existing membership without replacing it.
func (h *ProjectMembersHandler) UpdateMemberRole(
	w http.ResponseWriter,
	r *http.Request,
) {
	SetToastError(w, "Failed to update member role")
	id, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project ID", http.StatusBadRequest)
		return
	}
	if !auth.CanManageProject(r.Context(), h.repo, id) {
		auth.RenderUnauthorized(w, r)
		return
	}
	userID, err := strconv.ParseInt(chi.URLParam(r, "userId"), 10, 64)
	if err != nil || userID <= 0 {
		SetToastError(w, "Invalid user ID")
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		SetToastError(w, "Invalid form")
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	role := r.PostFormValue("role")
	if role != "admin" && role != "deployer" {
		SetToastError(w, "Role must be admin or deployer")
		http.Error(w, "Role must be admin or deployer",
			http.StatusUnprocessableEntity)
		return
	}
	if _, err := h.repo.Queries.GetProjectMember(r.Context(),
		db.GetProjectMemberParams{ProjectID: id, UserID: userID},
	); err != nil {
		if err == sql.ErrNoRows {
			SetToastError(w, "Member not found")
			http.Error(w, "Member not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.repo.Queries.AddProjectMember(r.Context(),
		db.AddProjectMemberParams{
			ProjectID: id, UserID: userID, Role: role,
		},
	); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if r.Header.Get("HX-Request") != "true" {
		http.Redirect(w, r, fmt.Sprintf("/projects/%d/edit", id),
			http.StatusSeeOther)
		return
	}
	members, available, canManage, err := loadProjectMembersContext(
		r, h.repo, id,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	SetToastSuccess(w, "Member role updated")
	if err := pages.ProjectMembersSection(
		db.Project{ID: id}, members, available, canManage, "Member role updated",
	).Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
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
	if isFragmentRequest(r) && r.Header.Get("X-Form-Dialog") != "true" {
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

	if !auth.CanManageProject(r.Context(), h.repo, id) {
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
	if r.Header.Get("HX-Request") == "true" &&
		r.Header.Get("X-Form-Dialog") != "true" {
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

	if !auth.CanManageProject(r.Context(), h.repo, id) {
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
	if r.Header.Get("HX-Request") == "true" &&
		r.Header.Get("X-Form-Dialog") != "true" {
		w.Header().Set("HX-Redirect", editURL)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, editURL, http.StatusSeeOther)
}
