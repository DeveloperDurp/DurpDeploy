package handler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/notify"
	"durpdeploy/internal/repository"
	"durpdeploy/views/pages"
)

type ProjectHandler struct {
	repo *repository.Repository
}

func NewProjectHandler(repo *repository.Repository) *ProjectHandler {
	return &ProjectHandler{repo: repo}
}

func (h *ProjectHandler) ListProjects(w http.ResponseWriter, r *http.Request) {
	if err := h.renderProjectsList(w, r); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// renderProjectsList is the shared render path used by the GET handler and
// by the post-update/post-delete HX responses. Loads the project list,
// builds per-project panels, and writes the table (or full page) HTML.
func (h *ProjectHandler) renderProjectsList(
	w http.ResponseWriter,
	r *http.Request,
) error {
	projects, err := h.projectsForUser(r)
	if err != nil {
		return err
	}
	panels := make([]pages.LifecyclePanel, len(projects))
	for i, p := range projects {
		panel, perr := h.buildPanelForProject(r, p, true)
		if perr != nil {
			return perr
		}
		panels[i] = panel
	}
	if isFragmentRequest(r) {
		return pages.ProjectsList(projects, panels).Render(r.Context(), w)
	}
	return pages.ProjectsListPage(projects, panels, r.URL.Path).
		Render(r.Context(), w)
}

// projectsForUser returns all projects for a global admin, or only the
// projects the user is a member of for any other role. P1-1: non-admins
// no longer see every project in the system.
func (h *ProjectHandler) projectsForUser(
	r *http.Request,
) ([]db.Project, error) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		return nil, nil
	}
	if user.Role == "admin" {
		return h.repo.Queries.ListProjects(r.Context())
	}
	return h.repo.Queries.ListProjectsForUser(r.Context(), user.ID)
}

func (h *ProjectHandler) NewProject(w http.ResponseWriter, r *http.Request) {
	lifecycles, err := h.repo.Queries.ListLifecycles(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := pages.ProjectFormPage(db.Project{}, false, "", lifecycles, nil, nil, false, r.URL.Path).
		Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *ProjectHandler) CreateProject(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	desc := r.FormValue("description")
	submitted := db.Project{
		Name:        name,
		Description: sql.NullString{String: desc, Valid: desc != ""},
	}
	if lifecycleID, parseErr := strconv.ParseInt(
		r.FormValue("lifecycle_id"),
		10,
		64,
	); parseErr == nil {
		submitted.LifecycleID = sql.NullInt64{Int64: lifecycleID, Valid: true}
	}
	if !h.authorizeLifecycleForm(w, r, 0) {
		return
	}

	if name == "" {
		lifecycles, _ := h.repo.Queries.ListLifecycles(r.Context())
		WriteFormError(
			w,
			r,
			pages.ProjectForm(
				submitted,
				false,
				"Name is required",
				lifecycles,
				nil, nil, false,
			),
			pages.ProjectFormPage(
				submitted,
				false,
				"Name is required",
				lifecycles,
				nil, nil, false,
				r.URL.Path,
			),
		)
		return
	}

	params := db.CreateProjectParams{
		Name: name,
		Description: sql.NullString{
			String: desc,
			Valid:  desc != "",
		},
	}

	// Create the project and add the creator as a per-project admin in a
	// single transaction, so a failed membership insert can't leave an
	// orphaned project the creator can't access (RequireProjectAccess
	// would 403 a creator without a membership row).
	var created db.Project
	err := h.repo.WithTx(r.Context(), func(q *db.Queries) error {
		var txErr error
		created, txErr = q.CreateProject(r.Context(), params)
		if txErr != nil {
			return txErr
		}
		if user := auth.UserFromContext(r.Context()); user != nil {
			txErr = q.AddProjectMember(
				r.Context(),
				db.AddProjectMemberParams{
					ProjectID: created.ID,
					UserID:    user.ID,
					Role:      "admin",
				},
			)
		}
		return txErr
	})
	if err != nil {
		if IsUniqueViolation(err) {
			lifecycles, _ := h.repo.Queries.ListLifecycles(r.Context())
			WriteFormError(
				w,
				r,
				pages.ProjectForm(
					submitted,
					false,
					"A project with this name already exists",
					lifecycles,
					nil, nil, false,
				),
				pages.ProjectFormPage(
					submitted,
					false,
					"A project with this name already exists",
					lifecycles,
					nil, nil, false,
					r.URL.Path,
				),
			)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := h.applyLifecycleSelection(r, created.ID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if retargetFormDialog(w, r, "#projects-list") {
		h.ListProjects(w, r)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/projects")
		return
	}
	http.Redirect(w, r, "/projects", http.StatusSeeOther)
}

// UpdateProjectNotifications saves the project's Slack webhook URL,
// comma-separated notify_emails list, Gotify server URL/token, and/or
// Discord webhook URL. Any field may be blank to disable that
// notifier. Notification URLs are validated before storage so deploy events
// cannot make server-side POSTs to local or private-network services.
func (h *ProjectHandler) UpdateProjectNotifications(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project ID", http.StatusBadRequest)
		return
	}

	webhook := strings.TrimSpace(r.FormValue("slack_webhook_url"))
	emails := strings.TrimSpace(r.FormValue("notify_emails"))
	gotifyURL := strings.TrimSpace(r.FormValue("gotify_url"))
	gotifyToken := strings.TrimSpace(r.FormValue("gotify_token"))
	discordWebhook := strings.TrimSpace(r.FormValue("discord_webhook_url"))

	if err := validateNotificationURLs(
		webhook,
		gotifyURL,
		discordWebhook,
	); err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}

	err = h.repo.Queries.UpdateProjectNotifications(
		r.Context(),
		db.UpdateProjectNotificationsParams{
			SlackWebhookUrl: sql.NullString{
				String: webhook,
				Valid:  webhook != "",
			},
			NotifyEmails: sql.NullString{String: emails, Valid: emails != ""},
			GotifyUrl: sql.NullString{
				String: gotifyURL,
				Valid:  gotifyURL != "",
			},
			GotifyToken: sql.NullString{
				String: gotifyToken,
				Valid:  gotifyToken != "",
			},
			DiscordWebhookUrl: sql.NullString{
				String: discordWebhook,
				Valid:  discordWebhook != "",
			},
			ID: id,
		},
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(
		w,
		r,
		fmt.Sprintf("/projects/%d/notifications", id),
		http.StatusSeeOther,
	)
}

// GetProjectNotifications renders the dedicated notifications settings page
// for a project (moved off the project overview so the overview stays
// focused on lifecycle/variables).
func (h *ProjectHandler) GetProjectNotifications(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project ID", http.StatusBadRequest)
		return
	}

	project, err := h.repo.Queries.GetProject(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Project not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := pages.ProjectNotificationsPage(project, r.URL.Path).
		Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *ProjectHandler) EditProject(w http.ResponseWriter, r *http.Request) {
	// The RequireProjectAccess middleware already validated and injected
	// the project id; fall back to re-parsing only if it's missing (e.g.
	// a misconfigured route not behind that middleware).
	id, ok := auth.ProjectIDFromContext(r.Context())
	if !ok {
		var err error
		id, err = parseProjectID(r)
		if err != nil {
			http.Error(w, "Invalid project ID", http.StatusBadRequest)
			return
		}
	}

	project, err := h.repo.Queries.GetProject(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Project not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	lifecycles, err := h.repo.Queries.ListLifecycles(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	members, available, canManage := h.loadMembersContext(r, id)

	if err := pages.ProjectFormPage(project, true, "", lifecycles, members, available, canManage, r.URL.Path).
		Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *ProjectHandler) UpdateProject(w http.ResponseWriter, r *http.Request) {
	id, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project ID", http.StatusBadRequest)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !h.authorizeLifecycleForm(w, r, id) {
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	desc := r.FormValue("description")

	if name == "" {
		project := db.Project{ID: id, Name: name}
		project.Description = sql.NullString{String: desc, Valid: desc != ""}
		if lifecycleID, parseErr := strconv.ParseInt(
			r.FormValue("lifecycle_id"),
			10,
			64,
		); parseErr == nil {
			project.LifecycleID = sql.NullInt64{Int64: lifecycleID, Valid: true}
		}
		lifecycles, _ := h.repo.Queries.ListLifecycles(r.Context())
		members, available, canManage := h.loadMembersContext(r, id)
		WriteFormError(
			w,
			r,
			pages.ProjectForm(
				project,
				true,
				"Name is required",
				lifecycles,
				members,
				available,
				canManage,
			),
			pages.ProjectFormPage(
				project,
				true,
				"Name is required",
				lifecycles,
				members, available, canManage,
				r.URL.Path,
			),
		)
		return
	}

	params := db.UpdateProjectParams{
		ID:   id,
		Name: name,
		Description: sql.NullString{
			String: desc,
			Valid:  desc != "",
		},
	}

	if _, err = h.repo.Queries.UpdateProject(r.Context(), params); err != nil {
		if IsUniqueViolation(err) {
			project := db.Project{ID: id, Name: name}
			project.Description = params.Description
			if lifecycleID, parseErr := strconv.ParseInt(
				r.FormValue("lifecycle_id"),
				10,
				64,
			); parseErr == nil {
				project.LifecycleID = sql.NullInt64{
					Int64: lifecycleID,
					Valid: true,
				}
			}
			lifecycles, _ := h.repo.Queries.ListLifecycles(r.Context())
			members, available, canManage := h.loadMembersContext(r, id)
			WriteFormError(
				w,
				r,
				pages.ProjectForm(
					project,
					true,
					"A project with this name already exists",
					lifecycles,
					members, available, canManage,
				),
				pages.ProjectFormPage(
					project,
					true,
					"A project with this name already exists",
					lifecycles,
					members, available, canManage,
					r.URL.Path,
				),
			)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := h.applyLifecycleSelection(r, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if retargetFormDialog(w, r, "#project-detail") {
		h.GetProject(w, r)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		// After an HTMX edit, navigate the browser back to the project's
		// detail page so the user sees the updated lifecycle / env panel.
		// HX-Redirect tells HTMX to do a full navigation rather than a
		// fragment swap, which would render the project page inside the
		// form's #form-container.
		w.Header().Set("HX-Redirect", fmt.Sprintf("/projects/%d", id))
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/projects/%d", id), http.StatusSeeOther)
}

// applyLifecycleSelection reads the lifecycle_id form field and updates the
// project's lifecycle accordingly. Empty string clears the lifecycle.
func (h *ProjectHandler) applyLifecycleSelection(
	r *http.Request,
	projectID int64,
) error {
	lifecycleStr := strings.TrimSpace(r.FormValue("lifecycle_id"))
	if lifecycleStr == "" {
		return ApplyLifecycleSelection(r.Context(), h.repo, projectID, 0)
	}
	id, err := strconv.ParseInt(lifecycleStr, 10, 64)
	if err != nil {
		return err
	}
	return ApplyLifecycleSelection(r.Context(), h.repo, projectID, id)
}

// ApplyLifecycleSelection sets or clears a project's lifecycle. It is exported
// so the JSON API handler can reuse the same logic without duplicating it.
func ApplyLifecycleSelection(
	ctx context.Context,
	repo *repository.Repository,
	projectID int64,
	lifecycleID int64,
) error {
	user := auth.UserFromContext(ctx)
	if lifecycleID <= 0 && (user == nil || user.Role != "admin") {
		project, err := repo.Queries.GetProject(ctx, projectID)
		if err != nil {
			return err
		}
		if !project.LifecycleID.Valid {
			return nil
		}
		if !CanManageProject(ctx, repo, user, projectID) {
			return ErrLifecycleRemovalForbidden
		}
		_, err = repo.Queries.ClearProjectLifecycleIfAssigned(
			ctx, db.ClearProjectLifecycleIfAssignedParams{
				ID: projectID, LifecycleID: project.LifecycleID,
			},
		)
		return err
	}
	if err := AuthorizeLifecycleSelection(
		ctx, repo, projectID, lifecycleID,
	); err != nil {
		return err
	}
	if lifecycleID <= 0 {
		return repo.Queries.ClearProjectLifecycle(ctx, projectID)
	}
	if user := auth.UserFromContext(ctx); user == nil || user.Role != "admin" {
		// Retaining a grant must not restore one revoked by a concurrent edit.
		return nil
	}
	return repo.Queries.SetProjectLifecycle(
		ctx,
		db.SetProjectLifecycleParams{
			LifecycleID: sql.NullInt64{Int64: lifecycleID, Valid: true},
			ID:          projectID,
		},
	)
}

func (h *ProjectHandler) DeleteProject(w http.ResponseWriter, r *http.Request) {
	id, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project ID", http.StatusBadRequest)
		return
	}

	if err = h.repo.DeleteProject(r.Context(), id); err != nil {
		if errors.Is(err, repository.ErrProjectHasActiveRunbook) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Both paths lead the user back to the projects list. The list page
	// has no Edit/Delete buttons anymore (those moved to the project
	// detail page), so callers from the project page expect a
	// navigation, not an in-place swap.
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/projects")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/projects", http.StatusSeeOther)
}

func parseProjectID(r *http.Request) (int64, error) {
	idStr := chi.URLParam(r, "id")
	return strconv.ParseInt(idStr, 10, 64)
}

func validateNotificationURLs(urls ...string) error {
	for _, raw := range urls {
		if err := notify.ValidateEndpointURL(raw); err != nil {
			return err
		}
	}
	return nil
}
