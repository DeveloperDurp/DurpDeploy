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

const projectsPath = "/projects"
const hxRedirectHeader = "HX-Redirect"
const projectNameRequired = "Name is required"
const projectNameConflict = "A project with this name already exists"

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

// projectsForUser also serves the project-template source picker.
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

// renderProjectsList is the shared render path used by the GET handler and
// by the post-update/post-delete HX responses. Loads the project list,
// builds per-project panels, and writes the table (or full page) HTML.
func (h *ProjectHandler) renderProjectsList(
	w http.ResponseWriter,
	r *http.Request,
) error {
	options, err := ParseProjectCatalogOptions(r.URL.Query())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return nil
	}
	access := ProjectCatalogAccess(r)
	projects, total, err := h.repo.ProjectCatalog(r.Context(), access, options)
	if err != nil {
		return err
	}
	groups, err := h.repo.Queries.ListAccessibleProjectGroups(
		r.Context(),
		access,
	)
	if err != nil {
		return err
	}
	state := pages.ProjectListState{
		Options: options,
		Groups:  groups,
		Total:   total,
		Path:    projectsPath,
	}
	var environments []db.Environment
	if len(projects) > 0 {
		environments, err = h.repo.Queries.ListEnvironments(r.Context())
		if err != nil {
			return err
		}
	}
	data, err := h.loadProjectPanelData(r, projects, environments)
	if err != nil {
		return err
	}
	panels := make([]pages.LifecyclePanel, len(projects))
	for i, p := range projects {
		panel, perr := h.buildPanelForProject(r, p, data, true)
		if perr != nil {
			return perr
		}
		panels[i] = panel
	}
	if isFragmentRequest(r) {
		return pages.ProjectsList(projects, panels, state).
			Render(r.Context(), w)
	}
	return pages.ProjectsListPage(projects, panels, state, r.URL.Path).
		Render(r.Context(), w)
}

func (h *ProjectHandler) NewProject(w http.ResponseWriter, r *http.Request) {
	h.renderNewProjectForm(w, r, db.Project{}, "")
}

func (h *ProjectHandler) renderNewProjectForm(
	w http.ResponseWriter,
	r *http.Request,
	project db.Project,
	errorMsg string,
) {
	lifecycles, err := h.repo.Queries.ListLifecycles(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	groups, err := h.repo.Queries.ListAccessibleProjectGroups(
		r.Context(), ProjectCatalogAccess(r),
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	page := pages.ProjectFormPage(project, false, errorMsg, lifecycles,
		groups, nil, nil, false, r.URL.Path)
	if errorMsg != "" {
		WriteFormError(w, r,
			pages.ProjectForm(project, false, errorMsg, lifecycles,
				groups, nil, nil, false),
			page,
		)
		return
	}
	if err := page.Render(r.Context(), w); err != nil {
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
	var groupID *int64
	if raw := r.FormValue("group_id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			h.renderNewProjectForm(w, r, submitted, invalidProjectGroupID)
			return
		}
		groupID = &id
		submitted.GroupID = sql.NullInt64{Int64: id, Valid: true}
	}

	if name == "" {
		h.renderNewProjectForm(w, r, submitted, projectNameRequired)
		return
	}

	params := db.CreateProjectParams{
		Name: name,
		Description: sql.NullString{
			String: desc,
			Valid:  desc != "",
		},
	}

	created, err := h.repo.CreateProject(
		r.Context(), params, ProjectCatalogAccess(r), groupID,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.renderNewProjectForm(w, r, submitted, "Select an available group")
			return
		}
		if IsUniqueViolation(err) {
			h.renderNewProjectForm(w, r, submitted,
				projectNameConflict)
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
		w.Header().Set("HX-Push-Url", r.URL.Path)
		h.ListProjects(w, r)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set(hxRedirectHeader, projectsPath)
		return
	}
	http.Redirect(w, r, projectsPath, http.StatusSeeOther)
}

// UpdateProjectNotifications saves the project's Slack webhook URL,
// comma-separated notify_emails list, Gotify server URL/token, and/or
// Discord webhook URL. Blank Gotify tokens require an unchanged destination.
// Explicit clear flags remove credentials. URLs are validated so deploy events
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

	webhook := notificationCredential(r, "slack_webhook_url")
	emails := strings.TrimSpace(r.FormValue("notify_emails"))
	gotifyURL := strings.TrimSpace(r.FormValue("gotify_url"))
	gotifyToken := strings.TrimSpace(r.FormValue("gotify_token"))
	discordWebhook := notificationCredential(r, "discord_webhook_url")

	if err := validateNotificationURLs(
		webhook,
		gotifyURL,
		discordWebhook,
	); err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}

	err = h.repo.UpdateProjectNotifications(
		r.Context(),
		db.UpdateProjectNotificationsParams{
			ClearSlackWebhookUrl: notificationClearFlag(
				r,
				"clear_slack_webhook_url",
			),
			ClearGotifyToken: notificationClearFlag(
				r,
				"clear_gotify_token",
			),
			ClearDiscordWebhookUrl: notificationClearFlag(
				r,
				"clear_discord_webhook_url",
			),
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
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(
			w,
			"could not save notification settings",
			http.StatusInternalServerError,
		)
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

	if err := pages.ProjectFormPage(project, true, "", lifecycles, nil, members, available, canManage, r.URL.Path).
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
				projectNameRequired,
				lifecycles,
				nil,
				members,
				available,
				canManage,
			),
			pages.ProjectFormPage(
				project,
				true,
				projectNameRequired,
				lifecycles,
				nil,
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

	lifecycleID, err := projectLifecycleID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err = UpdateProjectWithLifecycle(
		r.Context(), h.repo, params, lifecycleID,
	); err != nil {
		if errors.Is(err, ErrLifecycleAssignmentForbidden) ||
			errors.Is(err, ErrLifecycleRemovalForbidden) {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
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
					projectNameConflict,
					lifecycles,
					nil,
					members, available, canManage,
				),
				pages.ProjectFormPage(
					project,
					true,
					projectNameConflict,
					lifecycles,
					nil,
					members, available, canManage,
					r.URL.Path,
				),
			)
			return
		}
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
		w.Header().Set(hxRedirectHeader, fmt.Sprintf("/projects/%d", id))
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
	id, err := projectLifecycleID(r)
	if err != nil {
		return err
	}
	return ApplyLifecycleSelection(r.Context(), h.repo, projectID, id)
}

func projectLifecycleID(r *http.Request) (int64, error) {
	lifecycleStr := strings.TrimSpace(r.FormValue("lifecycle_id"))
	if lifecycleStr == "" {
		return 0, nil
	}
	return strconv.ParseInt(lifecycleStr, 10, 64)
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
		if !auth.CanManageProject(ctx, repo, projectID) {
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
		w.Header().Set(hxRedirectHeader, projectsPath)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, projectsPath, http.StatusSeeOther)
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
