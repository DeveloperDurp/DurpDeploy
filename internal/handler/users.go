package handler

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/views/pages"
)

// UsersHandler manages /admin/users. All routes are admin-only — the
// RequireRole("admin") middleware on the /admin/* sub-group in
// server.go gates entry.
//
// ponytail: this is admin-only CRUD. The self-delete + "user has
// project members" guards live here (handler-level), not in the
// middleware, so the failure modes are visible at the call site
// (WriteFormError renders a 422 with a clear message). Moving them
// into middleware would only save one line per handler and lose the
// human-readable error path.
type UsersHandler struct {
	repo *repository.Repository
}

func NewUsersHandler(repo *repository.Repository) *UsersHandler {
	return &UsersHandler{repo: repo}
}

// validRoles is the set of role values accepted by the create/update
// forms. Matches the CHECK constraint in migrations/011_auth.sql.
var validRoles = map[string]bool{
	"admin":    true,
	"deployer": true,
	"viewer":   true,
}

// ListUsers renders /admin/users with every user in the system.
func (h *UsersHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.repo.Queries.ListUsers(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	currentUser := auth.UserFromContext(r.Context())
	if err := pages.UsersListPage(
		users,
		currentUser,
		r.URL.Path,
	).Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// NewUserForm renders the create-user form at /admin/users/new.
func (h *UsersHandler) NewUserForm(w http.ResponseWriter, r *http.Request) {
	if err := pages.UserFormPage(nil, "", r.URL.Path).
		Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// CreateUser handles POST /admin/users. Hashes the password with
// argon2id (same path as the CLI `admin create`), then redirects to
// /admin/users without redisplaying the plaintext password.
// The audit middleware records the row as "create_user" via the
// actionMap entry.
func (h *UsersHandler) CreateUser(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	email := strings.TrimSpace(r.FormValue("email"))
	name := strings.TrimSpace(r.FormValue("name"))
	role := r.FormValue("role")
	password := r.FormValue("password")
	passwordConfirmation := r.FormValue("password_confirmation")

	errMsg := validateUserFields(
		email, name, role, password, passwordConfirmation, true,
	)
	if errMsg != "" {
		h.renderFormError(
			w,
			r,
			&db.User{Email: email, Name: name, Role: role},
			errMsg,
			true,
		)
		return
	}

	existing, err := h.repo.Queries.GetUserByEmail(r.Context(), email)
	if err == nil && existing.ID != 0 {
		h.renderFormError(
			w, r,
			&db.User{Email: email, Name: name, Role: role},
			"A user with that email already exists",
			true,
		)
		return
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	_, err = h.repo.Queries.CreateUser(r.Context(), db.CreateUserParams{
		Email:        email,
		PasswordHash: hash,
		Name:         name,
		Role:         role,
	})
	if err != nil {
		h.renderFormError(
			w, r,
			&db.User{Email: email, Name: name, Role: role},
			"Could not create user (the email may be taken)",
			true,
		)
		return
	}

	redirect := "/admin/users"

	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", redirect)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}

// EditUserForm renders /admin/users/{id}/edit with the user loaded
// from the DB.
func (h *UsersHandler) EditUserForm(w http.ResponseWriter, r *http.Request) {
	id, err := parseUserID(r)
	if err != nil {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}

	u, err := h.repo.Queries.GetUserByID(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := pages.UserFormPage(&u, "", r.URL.Path).
		Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// UpdateUser handles PUT /admin/users/{id}. Updates name + role, and
// optionally the password (blank = keep current). When the role
// changes, all of the user's sessions are deleted so the new role
// takes effect on the user's next request.
func (h *UsersHandler) UpdateUser(w http.ResponseWriter, r *http.Request) {
	id, err := parseUserID(r)
	if err != nil {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}

	target, err := h.repo.Queries.GetUserByID(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	role := r.FormValue("role")
	password := r.FormValue("password")
	passwordConfirmation := r.FormValue("password_confirmation")

	errMsg := validateUserFields(
		emailKeepCurrent(target.Email),
		name,
		role,
		password,
		passwordConfirmation,
		false,
	)
	if errMsg != "" {
		target.Name = name
		target.Role = role
		h.renderFormError(w, r, &target, errMsg, false)
		return
	}

	if err := h.repo.Queries.UpdateUser(r.Context(), db.UpdateUserParams{
		Name: name,
		Role: role,
		ID:   id,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if password != "" {
		if err := auth.UpdatePassword(r.Context(), h.repo, auth.PasswordChange{
			UserID:   id,
			Password: password,
		}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	if role != target.Role && password == "" {
		if err := auth.InvalidateBrowserAuth(
			r.Context(),
			h.repo,
			id,
		); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	redirect := "/admin/users"

	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", redirect)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}

// DeleteUser handles DELETE /admin/users/{id}. Refuses to delete the
// current user (prevents the "I just deleted the only admin" footgun)
// and refuses to delete a user who still has project_members rows
// (forces the admin to remove project membership first).
//
// ponytail: the project_members check uses ListProjectsForUser which
// is the existing query P1-1 added. No new query needed; an empty
// slice means no memberships. If the project list grows large this
// becomes a SELECT N rows for a 0/1 check — a dedicated
// CountUserProjects query would be cheaper but is YAGNI for 5-10
// users.
func (h *UsersHandler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := parseUserID(r)
	if err != nil {
		http.Error(w, "Invalid user ID", http.StatusBadRequest)
		return
	}

	current := auth.UserFromContext(r.Context())
	if current != nil && current.ID == id {
		http.Error(
			w,
			"You cannot delete your own account",
			http.StatusUnprocessableEntity,
		)
		return
	}

	target, err := h.repo.Queries.GetUserByID(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	projects, err := h.repo.Queries.ListProjectsForUser(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if len(projects) > 0 {
		http.Error(
			w,
			"User still has project memberships; remove them first",
			http.StatusUnprocessableEntity,
		)
		return
	}

	// Delete sessions first so the user's in-flight requests fail
	// cleanly before the user row goes away. sessions.id FK has
	// ON DELETE CASCADE so the user delete would clean them up too,
	// but doing it explicitly makes the intent obvious and the
	// CASCADE documentation a non-load-bearing detail.
	_ = h.repo.Queries.DeleteSessionsByUser(r.Context(), id)

	if err := h.repo.Queries.DeleteUser(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Reference the target so the var is "used" (target was loaded for
	// the 404 path and to populate the audit log details).
	_ = target

	redirect := "/admin/users"
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", redirect)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}

// renderFormError centralizes the 422-with-form re-render for both
// the create and update paths. The isNew flag picks the form URL and
// the form's submit label.
func (h *UsersHandler) renderFormError(
	w http.ResponseWriter,
	r *http.Request,
	u *db.User,
	msg string,
	isNew bool,
) {
	WriteFormError(
		w,
		r,
		pages.UserFormFragment(u, msg, isNew),
		pages.UserFormPage(u, msg, r.URL.Path),
	)
}

// emailKeepCurrent returns a syntactically-valid placeholder so the
// shared validator (which requires a non-empty email) doesn't reject
// a submitted edit form that intentionally didn't touch the email.
// The edit form doesn't carry the email field at all (we don't allow
// email changes in P1), so this value is only used to feed the
// validator — the DB keeps the original.
func emailKeepCurrent(original string) string {
	if original == "" {
		return "placeholder@example.invalid"
	}
	return original
}

// validateUserFields returns "" when valid, otherwise a human-readable
// error message. isNew=true requires a non-empty password; the edit
// path treats a blank password as "keep current".
func validateUserFields(
	email, name, role, password, passwordConfirmation string,
	isNew bool,
) string {
	if email == "" || !strings.Contains(email, "@") {
		return "Email is required and must look like an email"
	}
	if name == "" {
		return "Name is required"
	}
	if !validRoles[role] {
		return "Role must be admin, deployer, or viewer"
	}
	if isNew {
		if password == "" {
			return "Password is required"
		}
		if passwordConfirmation == "" {
			return "Password confirmation is required"
		}
		if len(password) < 8 {
			return "Password must be at least 8 characters"
		}
	} else {
		if password == "" && passwordConfirmation != "" {
			return "New password is required when confirming a password"
		}
		if password != "" && passwordConfirmation == "" {
			return "Password confirmation is required"
		}
		if password != "" && len(password) < 8 {
			return "New password must be at least 8 characters (blank = keep current)"
		}
	}
	if password != passwordConfirmation {
		return "Passwords do not match"
	}
	return ""
}

func parseUserID(r *http.Request) (int64, error) {
	idStr := chi.URLParam(r, "id")
	return strconv.ParseInt(idStr, 10, 64)
}
