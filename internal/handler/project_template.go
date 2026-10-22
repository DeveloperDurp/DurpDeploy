package handler

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/audit"
	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/roles"
	"durpdeploy/views/pages"
)

const projectTemplatesPath = "/templates?type=projects"

type ProjectTemplateHandler struct{ repo *repository.Repository }

func NewProjectTemplateHandler(
	repo *repository.Repository,
) *ProjectTemplateHandler {
	return &ProjectTemplateHandler{repo: repo}
}

func ProjectTemplateActor(r *http.Request) repository.ProjectTemplateActor {
	actor := repository.ProjectTemplateActor{
		Role:   auth.RoleFromContext(r.Context()),
		Scoped: auth.ScopedTokenID(r.Context()) != "",
	}
	if user := auth.UserFromContext(r.Context()); user != nil {
		actor.UserID = user.ID
	}
	return actor
}

func (h *ProjectTemplateHandler) List(w http.ResponseWriter, r *http.Request) {
	projectID, _ := auth.ProjectIDFromContext(r.Context())
	offset := int64(0)
	if raw := r.URL.Query().Get("offset"); raw != "" {
		var err error
		offset, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || offset < 0 {
			http.Error(w, "Invalid offset", 400)
			return
		}
	}
	if r.URL.Path == "/project-templates" {
		destination := projectTemplatesPath
		if offset > 0 {
			destination += "&offset=" + strconv.FormatInt(offset, 10)
		}
		http.Redirect(w, r, destination, http.StatusSeeOther)
		return
	}
	actor := ProjectTemplateActor(r)
	admin := int64(0)
	if user := auth.UserFromContext(
		r.Context(),
	); user != nil &&
		user.Role == "admin" {
		admin = 1
	}
	rows, err := h.repo.Queries.ListAccessibleProjectTemplates(
		r.Context(),
		db.ListAccessibleProjectTemplatesParams{
			SourceProjectID: projectID,
			IsAdmin:         admin,
			UserID:          actor.UserID,
			PageLimit:       50,
			PageOffset:      offset,
		},
	)
	if err != nil {
		http.Error(w, "Could not list project templates", 500)
		return
	}
	total, err := h.repo.Queries.CountAccessibleProjectTemplates(
		r.Context(),
		db.CountAccessibleProjectTemplatesParams{
			SourceProjectID: projectID,
			IsAdmin:         admin,
			UserID:          actor.UserID,
		},
	)
	if err != nil {
		http.Error(w, "Could not count project templates", 500)
		return
	}
	page := pages.ProjectTemplatesPage(rows, offset, total, r.URL.Path)
	if isFragmentRequest(r) {
		page = pages.ProjectTemplatesListContent(
			rows,
			offset,
			total,
			r.URL.Path,
		)
	}
	if err := page.
		Render(r.Context(), w); err != nil {
		http.Error(w, "Could not render project templates", 500)
	}
}

func (h *ProjectTemplateHandler) Form(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, repository.ApplyProjectTemplate{}, "", 200, true)
}

func (h *ProjectTemplateHandler) Sources(
	w http.ResponseWriter,
	r *http.Request,
) {
	projects, err := (&ProjectHandler{repo: h.repo}).projectsForUser(r)
	if err != nil {
		http.Error(w, "Could not list source projects", 500)
		return
	}
	user := auth.UserFromContext(r.Context())
	sources := make([]db.Project, 0, len(projects))
	for _, project := range projects {
		if user.Role == "admin" {
			sources = append(sources, project)
			continue
		}
		member, err := h.repo.Queries.GetProjectMember(
			r.Context(),
			db.GetProjectMemberParams{ProjectID: project.ID, UserID: user.ID},
		)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			http.Error(w, "Could not check source-project access", 500)
			return
		}
		if roles.AtLeast(member.Role, "maintainer") {
			sources = append(sources, project)
		}
	}
	if err := pages.ProjectTemplateSourcesPage(sources, r.URL.Path).Render(r.Context(), w); err != nil {
		http.Error(w, "Could not render source projects", 500)
	}
}

func (h *ProjectTemplateHandler) Preview(
	w http.ResponseWriter,
	r *http.Request,
) {
	h.render(w, r, repository.ApplyProjectTemplate{}, "", 200, true)
}

func (h *ProjectTemplateHandler) formData(
	r *http.Request,
) (pages.ProjectTemplateFormData, error) {
	projectID, _ := auth.ProjectIDFromContext(r.Context())
	data := pages.ProjectTemplateFormData{
		ProjectID: projectID,
		ReturnTo:  projectTemplateReturnPath(r, projectID),
	}
	var err error
	if raw := chi.URLParam(r, "templateId"); raw != "" {
		id, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil || id <= 0 {
			return data, repository.ErrProjectTemplateInvalid
		}
		data.Template, err = h.repo.Queries.GetProjectTemplate(
			r.Context(),
			db.GetProjectTemplateParams{SourceProjectID: projectID, ID: id},
		)
		if err != nil {
			return data, err
		}
		data.Snapshot, err = h.repo.ReadProjectTemplate(data.Template)
	} else {
		data.Snapshot, err = h.repo.CaptureProjectTemplate(
			r.Context(),
			projectID,
		)
	}
	if err != nil {
		return data, err
	}
	data.Environments, err = h.repo.Queries.ListEnvironments(r.Context())
	if err != nil {
		return data, err
	}
	if auth.RoleFromContext(r.Context()) == "admin" {
		data.Lifecycles, err = h.repo.Queries.ListLifecycles(r.Context())
	}
	return data, err
}

func projectTemplateReturnPath(r *http.Request, projectID int64) string {
	if r.FormValue("return_to") == projectTemplatesPath {
		return projectTemplatesPath
	}
	return fmt.Sprintf("/projects/%d", projectID)
}

func (h *ProjectTemplateHandler) render(
	w http.ResponseWriter,
	r *http.Request,
	input repository.ApplyProjectTemplate,
	message string,
	status int,
	defaults bool,
) {
	data, err := h.formData(r)
	if err != nil {
		code, text := projectTemplateWebError(err)
		http.Error(w, text, code)
		return
	}
	if defaults {
		input.Description = &data.Snapshot.Description
	}
	data.Input, data.Error = input, message
	w.WriteHeader(status)
	if err := pages.ProjectTemplatePage(data, r.URL.Path).
		Render(r.Context(), w); err != nil {
		http.Error(w, "Could not render project template", 500)
	}
}

func (h *ProjectTemplateHandler) Save(w http.ResponseWriter, r *http.Request) {
	projectID, _ := auth.ProjectIDFromContext(r.Context())
	name, description := r.FormValue("name"), r.FormValue("description")
	row, err := h.repo.SaveProjectTemplate(
		r.Context(),
		ProjectTemplateActor(r),
		projectID,
		name,
		description,
	)
	if err != nil {
		code, message := projectTemplateWebError(err)
		h.render(
			w,
			r,
			repository.ApplyProjectTemplate{
				Name:        name,
				Description: &description,
			},
			message,
			code,
			false,
		)
		return
	}
	audit.SetProjectTemplateResult(
		r,
		"create_project_template",
		"project_template",
		row.ID,
	)
	http.Redirect(
		w,
		r,
		projectTemplateReturnPath(r, projectID),
		http.StatusSeeOther,
	)
}

func (h *ProjectTemplateHandler) Apply(w http.ResponseWriter, r *http.Request) {
	data, err := h.formData(r)
	if err != nil {
		code, message := projectTemplateWebError(err)
		http.Error(w, message, code)
		return
	}
	input, err := projectTemplateFormInput(r, data.Snapshot)
	if err == nil {
		var created db.Project
		created, err = h.repo.CreateProjectFromTemplate(
			r.Context(),
			ProjectTemplateActor(r),
			data.ProjectID,
			data.Template.ID,
			input,
		)
		if err == nil {
			audit.SetProjectTemplateResult(
				r,
				"create_project_from_template",
				"project",
				created.ID,
			)
			http.Redirect(
				w,
				r,
				fmt.Sprintf("/projects/%d", created.ID),
				http.StatusSeeOther,
			)
			return
		}
	}
	code, message := projectTemplateWebError(err)
	h.render(w, r, input, message, code, false)
}

func projectTemplateFormInput(
	r *http.Request,
	snapshot repository.ProjectTemplateSnapshot,
) (repository.ApplyProjectTemplate, error) {
	description := r.FormValue("description")
	input := repository.ApplyProjectTemplate{
		Name:                r.FormValue("name"),
		Description:         &description,
		Reviewed:            r.FormValue("reviewed") == "true",
		VariableValues:      map[string]string{},
		EnvironmentMappings: map[string]int64{},
		AgentSelectors:      map[string][]string{},
	}
	lifecycleID, err := strconv.ParseInt(r.FormValue("lifecycle_id"), 10, 64)
	if err != nil || lifecycleID < 0 {
		return input, repository.ErrProjectTemplateInvalid
	}
	input.LifecycleID = lifecycleID
	if err := projectTemplateVariableInputs(
		r,
		snapshot.Variables,
		&input,
	); err != nil {
		return input, err
	}
	for i, step := range snapshot.Steps {
		if step.ExecutionTarget != "agent" {
			continue
		}
		key := strconv.Itoa(i + 1)
		input.AgentSelectors[key] = []string{}
		for _, label := range strings.Split(r.FormValue("selectors_"+key), ",") {
			if label = strings.ToLower(strings.TrimSpace(label)); label != "" {
				input.AgentSelectors[key] = append(
					input.AgentSelectors[key],
					label,
				)
			}
		}
	}
	if snapshot.PackageRepository != nil {
		input.PackageRepository = &repository.ProjectTemplatePackage{
			URLTemplate: r.FormValue("package_url"),
			AuthType:    r.FormValue("package_auth"),
			Username:    r.FormValue("package_username"),
			Credential:  r.FormValue("package_credential"),
		}
	}
	return input, nil
}

func projectTemplateVariableInputs(
	r *http.Request,
	variables []repository.ProjectTemplateVariable,
	input *repository.ApplyProjectTemplate,
) error {
	for _, v := range variables {
		if v.Secret || r.FormValue("override_"+v.Key) == "true" {
			input.VariableValues[v.Key] = r.FormValue("variable_" + v.Key)
		}
		if v.EnvironmentID != 0 {
			key := strconv.FormatInt(v.EnvironmentID, 10)
			id, err := strconv.ParseInt(r.FormValue("environment_"+key), 10, 64)
			if err != nil || id <= 0 {
				return repository.ErrProjectTemplateInvalid
			}
			input.EnvironmentMappings[key] = id
		}
	}
	return nil
}

func projectTemplateWebError(err error) (int, string) {
	switch {
	case errors.Is(err, repository.ErrProjectTemplateAccess):
		return 403, "Project template access denied"
	case errors.Is(err, repository.ErrProjectTemplateInvalid):
		return 422, err.Error()
	case errors.Is(err, sql.ErrNoRows):
		return 404, "Project template not found"
	case IsUniqueViolation(err):
		return 422, "A project or template with this name already exists"
	default:
		return 500, "Could not process project template"
	}
}
