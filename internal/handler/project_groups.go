package handler

import (
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"durpdeploy/internal/audit"
	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/roles"
	"durpdeploy/views/pages"
)

const invalidProjectGroupID = "Invalid group ID"
const projectGroupsPath = "/project-groups"
const hxRequestHeader = "HX-Request"

func ProjectCatalogAccess(r *http.Request) repository.ProjectCatalogAccess {
	access := repository.ProjectCatalogAccess{
		TokenID: auth.ScopedTokenID(r.Context()),
	}
	if user := auth.UserFromContext(r.Context()); user != nil {
		access.UserID = user.ID
		if user.Role == "admin" {
			access.IsAdmin = 1
		}
	}
	return access
}

// ParseProjectCatalogOptions shares list semantics between the API and browser.
func ParseProjectCatalogOptions(
	values url.Values,
) (repository.ProjectCatalogOptions, error) {
	o := repository.ProjectCatalogOptions{
		Query:   strings.TrimSpace(values.Get("q")),
		Sort:    values.Get("sort"),
		GroupID: -1,
		Limit:   100,
	}
	if len(o.Query) > 1000 {
		return o, errors.New("search must not exceed 1000 bytes")
	}
	if o.Sort == "" {
		o.Sort = "name"
	}
	if o.Sort != "newest" && o.Sort != "name" && o.Sort != "name_desc" {
		return o, errors.New("sort must be newest, name, or name_desc")
	}
	for _, item := range []struct {
		key              string
		value            *int64
		minimum, maximum int64
	}{
		{"group_id", &o.GroupID, 0, 1<<63 - 1},
		{"limit", &o.Limit, 1, 1000}, {"offset", &o.Offset, 0, 1<<63 - 1},
	} {
		if raw := values.Get(item.key); raw != "" {
			n, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || n < item.minimum || n > item.maximum {
				return o, errors.New("invalid " + item.key)
			}
			*item.value = n
		}
	}
	return o, nil
}

func ProjectGroupErrorStatus(err error) int {
	switch {
	case errors.Is(err, repository.ErrProjectGroupName):
		return http.StatusUnprocessableEntity
	case errors.Is(err, sql.ErrNoRows):
		return http.StatusNotFound
	case IsUniqueViolation(err):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func (h *ProjectHandler) ManageProjectGroups(
	w http.ResponseWriter,
	r *http.Request,
) {
	options, err := ParseProjectCatalogOptions(r.URL.Query())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	access := ProjectCatalogAccess(r)
	projects, total, err := h.repo.ProjectCatalog(r.Context(), access, options)
	if err != nil {
		http.Error(w, "could not list projects", 500)
		return
	}
	groups, err := h.repo.Queries.ListAccessibleProjectGroups(
		r.Context(),
		access,
	)
	if err != nil {
		http.Error(w, "could not list groups", 500)
		return
	}
	canMove, err := h.projectMovePermissions(r, projects)
	if err != nil {
		http.Error(w, "could not load project permissions", 500)
		return
	}
	_, ungrouped, err := h.repo.ProjectCatalog(
		r.Context(), access,
		repository.ProjectCatalogOptions{GroupID: 0, Limit: 1, Sort: "newest"},
	)
	if err != nil {
		http.Error(w, "could not count ungrouped projects", 500)
		return
	}
	state := pages.ProjectListState{
		Path: projectGroupsPath, Options: options, Groups: groups, Total: total, CanMove: canMove,
		Ungrouped: ungrouped,
		Modal: r.Header.Get(hxRequestHeader) == "true" &&
			r.Header.Get("X-Group-Dialog") == "true",
	}
	page := pages.ProjectGroupsPage(projects, state, r.URL.Path)
	if state.Modal {
		page = pages.ProjectGroupWorkspace(projects, state, r.URL.Path)
	}
	if err := page.Render(r.Context(), w); err != nil {
		http.Error(w, "could not render groups", 500)
	}
}

func (h *ProjectHandler) projectMovePermissions(
	r *http.Request,
	projects []db.Project,
) (map[int64]bool, error) {
	canMove := make(map[int64]bool)
	if !auth.CanAuthor(r.Context()) {
		return canMove, nil
	}
	access := ProjectCatalogAccess(r)
	for _, project := range projects {
		if access.IsAdmin == 1 {
			canMove[project.ID] = true
			continue
		}
		member, err := h.repo.Queries.GetProjectMember(
			r.Context(),
			db.GetProjectMemberParams{
				ProjectID: project.ID, UserID: access.UserID,
			},
		)
		if err != nil {
			return nil, err
		}
		canMove[project.ID] = roles.AtLeast(member.Role, "maintainer")
	}
	return canMove, nil
}

func (h *ProjectHandler) SaveProjectGroup(
	w http.ResponseWriter,
	r *http.Request,
) {
	var id int64
	if raw := r.FormValue("id"); raw != "" {
		var err error
		id, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			http.Error(w, invalidProjectGroupID, 400)
			return
		}
	}
	group, err := h.repo.SaveProjectGroup(r.Context(), id, r.FormValue("name"))
	if err != nil {
		http.Error(w, err.Error(), ProjectGroupErrorStatus(err))
		return
	}
	audit.SetProjectGroupChange(r, group.ID, group.Name, nil)
	if projectGroupDialogResponse(w, r, group.ID) {
		return
	}
	http.Redirect(
		w, r, projectGroupsPath+"?group_id="+strconv.FormatInt(group.ID, 10),
		http.StatusSeeOther,
	)
}

func (h *ProjectHandler) DeleteProjectGroup(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, err := parseProjectID(r)
	if err != nil || id <= 0 {
		http.Error(w, invalidProjectGroupID, 400)
		return
	}
	deleted, err := h.repo.Queries.DeleteProjectGroup(r.Context(), id)
	if err != nil {
		http.Error(w, "could not delete group", 500)
		return
	}
	if deleted == 0 {
		http.NotFound(w, r)
		return
	}
	audit.SetProjectGroupChange(r, id, "", nil)
	if projectGroupDialogResponse(w, r, 0) {
		return
	}
	http.Redirect(w, r, projectGroupsPath, http.StatusSeeOther)
}

func projectGroupDialogResponse(
	w http.ResponseWriter, r *http.Request, groupID int64,
) bool {
	if r.Header.Get(hxRequestHeader) != "true" ||
		r.Header.Get("X-Group-Dialog") != "true" {
		return false
	}
	location := projectGroupsPath
	if groupID > 0 {
		location += "?group_id=" + strconv.FormatInt(groupID, 10)
	}
	// The location contains only a fixed path and a decimal group ID.
	w.Header().Set("HX-Trigger", `{"project-group-saved":{"url":`+
		strconv.Quote(location)+`}}`)
	w.WriteHeader(http.StatusNoContent)
	return true
}

func (h *ProjectHandler) MoveProjectGroup(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, ok := auth.ProjectIDFromContext(r.Context())
	if !ok {
		http.Error(w, "Invalid project ID", 400)
		return
	}
	var groupID *int64
	if raw := r.FormValue("group_id"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n <= 0 {
			http.Error(w, invalidProjectGroupID, http.StatusUnprocessableEntity)
			return
		}
		groupID = &n
	}
	_, err := h.repo.MoveProjectGroup(
		r.Context(),
		ProjectCatalogAccess(r),
		id,
		groupID,
	)
	if err != nil {
		http.Error(w, "could not move project", ProjectGroupErrorStatus(err))
		return
	}
	audit.SetProjectGroupChange(r, id, "", groupID)
	if r.Header.Get(hxRequestHeader) == "true" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, projectGroupsPath, http.StatusSeeOther)
}
