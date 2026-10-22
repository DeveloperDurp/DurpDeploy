package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/db"
	"durpdeploy/internal/roles"
)

var (
	ErrProjectTemplateInvalid = errors.New(
		"invalid project template configuration",
	)
	ErrProjectTemplateAccess = errors.New("project template access denied")
)

// ProjectTemplateActor carries the HTTP permission ceiling into the transaction.
type ProjectTemplateActor struct {
	UserID int64
	Role   string
	Scoped bool
}

type ProjectTemplateStep struct {
	Name                 string   `json:"name"`
	ScriptBody           string   `json:"script_body"`
	Interpreter          string   `json:"interpreter"`
	SortOrder            int64    `json:"sort_order"`
	TimeoutSeconds       int64    `json:"timeout_seconds"`
	MaxRetries           int64    `json:"max_retries"`
	ExecutionTarget      string   `json:"execution_target"`
	AgentExecutionMode   string   `json:"agent_execution_mode"`
	AgentSelectors       []string `json:"agent_selectors"`
	ContainerImage       string   `json:"container_image"`
	NetworkMode          string   `json:"network_mode"`
	ApprovalArtifactPath string   `json:"approval_artifact_path"`
	ApprovalReviewPath   string   `json:"approval_review_path"`
	ApprovalReviewFormat string   `json:"approval_review_format"`
	VariableNames        []string `json:"variable_names"`
}

type ProjectTemplateVariable struct {
	Key             string  `json:"key"`
	Name            string  `json:"name"`
	Default         *string `json:"default,omitempty"`
	Secret          bool    `json:"secret"`
	EnvironmentID   int64   `json:"environment_id"`
	EnvironmentName string  `json:"environment_name"`
}

// ProjectTemplateSnapshot excludes credentials and execution history by construction.
type ProjectTemplateSnapshot struct {
	Name              string                    `json:"name"`
	Description       string                    `json:"description"`
	LifecycleName     string                    `json:"lifecycle_name"`
	Steps             []ProjectTemplateStep     `json:"steps"`
	Variables         []ProjectTemplateVariable `json:"variables"`
	PackageRepository *artifact.Repository      `json:"package_repository,omitempty"`
}

type ApplyProjectTemplate struct {
	Name                string                  `json:"name"`
	Description         *string                 `json:"description,omitempty"`
	LifecycleID         int64                   `json:"lifecycle_id"`
	Reviewed            bool                    `json:"reviewed"`
	EnvironmentMappings map[string]int64        `json:"environment_mappings"`
	VariableValues      map[string]string       `json:"variable_values"`
	AgentSelectors      map[string][]string     `json:"agent_selectors"`
	PackageRepository   *ProjectTemplatePackage `json:"package_repository"`
}

type ProjectTemplatePackage struct {
	URLTemplate string `json:"url_template"`
	AuthType    string `json:"auth_type"`
	Username    string `json:"username"`
	Credential  string `json:"credential"`
}

func templateInvalid(message string) error {
	return fmt.Errorf("%w: %s", ErrProjectTemplateInvalid, message)
}

// templateTx keeps source fields, selectors, variables, and grants in one view.
func (r *Repository) templateTx(
	ctx context.Context,
	fn func(*db.Queries) error,
) error {
	return withSQLiteBusyRetry(ctx, func() error {
		tx, err := r.DB.BeginTx(
			ctx,
			&sql.TxOptions{Isolation: sql.LevelSerializable},
		)
		if err != nil {
			return err
		}
		defer func() { LogRollbackError(tx.Rollback()) }()
		if err := fn(r.Queries.WithTx(tx)); err != nil {
			return err
		}
		return tx.Commit()
	})
}

func templateAuthor(
	ctx context.Context,
	q *db.Queries,
	actor ProjectTemplateActor,
	projectID int64,
) (bool, error) {
	user, err := q.GetUserByID(ctx, actor.UserID)
	if err != nil {
		return false, err
	}
	if !roles.AtLeast(user.Role, "maintainer") ||
		!roles.AtLeast(actor.Role, "maintainer") {
		return false, ErrProjectTemplateAccess
	}
	admin := user.Role == "admin" && actor.Role == "admin" && !actor.Scoped
	if user.Role != "admin" {
		member, err := q.GetProjectMember(
			ctx,
			db.GetProjectMemberParams{
				ProjectID: projectID,
				UserID:    actor.UserID,
			},
		)
		if errors.Is(err, sql.ErrNoRows) {
			return false, ErrProjectTemplateAccess
		}
		if err != nil {
			return false, err
		}
		if !roles.AtLeast(member.Role, "maintainer") {
			return false, ErrProjectTemplateAccess
		}
	}
	return admin, nil
}

func (r *Repository) CaptureProjectTemplate(
	ctx context.Context,
	projectID int64,
) (ProjectTemplateSnapshot, error) {
	var snapshot ProjectTemplateSnapshot
	err := r.templateTx(ctx, func(q *db.Queries) error {
		var err error
		snapshot, err = r.captureProjectTemplate(ctx, q, projectID)
		return err
	})
	return snapshot, err
}

func (r *Repository) captureProjectTemplate(
	ctx context.Context,
	q *db.Queries,
	projectID int64,
) (ProjectTemplateSnapshot, error) {
	p, err := q.GetProject(ctx, projectID)
	if err != nil {
		return ProjectTemplateSnapshot{}, err
	}
	snapshot := ProjectTemplateSnapshot{
		Name:        p.Name,
		Description: p.Description.String,
		Steps:       []ProjectTemplateStep{},
		Variables:   []ProjectTemplateVariable{},
	}
	if p.LifecycleID.Valid {
		lc, err := q.GetLifecycle(ctx, p.LifecycleID.Int64)
		if err != nil {
			return snapshot, err
		}
		snapshot.LifecycleName = lc.Name
	}
	steps, err := q.ListStepsByProject(ctx, projectID)
	if err != nil {
		return snapshot, err
	}
	ids := make([]int64, len(steps))
	for i, step := range steps {
		ids[i] = step.ID
	}
	selectors, err := StepSelectors(ctx, q, ids)
	if err != nil {
		return snapshot, err
	}
	for _, s := range steps {
		names := []string{}
		if s.VariableNames != "" {
			if err := json.Unmarshal(
				[]byte(s.VariableNames),
				&names,
			); err != nil {
				return snapshot, err
			}
		}
		if names == nil {
			names = []string{}
		}
		if selectors[s.ID] == nil {
			selectors[s.ID] = []string{}
		}
		snapshot.Steps = append(snapshot.Steps, ProjectTemplateStep{
			Name:                 s.Name,
			ScriptBody:           s.ScriptBody,
			Interpreter:          s.Interpreter,
			SortOrder:            s.SortOrder,
			TimeoutSeconds:       s.TimeoutSeconds,
			MaxRetries:           s.MaxRetries,
			ExecutionTarget:      s.ExecutionTarget,
			AgentExecutionMode:   s.AgentExecutionMode,
			AgentSelectors:       selectors[s.ID],
			ContainerImage:       s.ContainerImage,
			NetworkMode:          s.NetworkMode,
			ApprovalArtifactPath: s.ApprovalArtifactPath,
			ApprovalReviewPath:   s.ApprovalReviewPath,
			ApprovalReviewFormat: s.ApprovalReviewFormat,
			VariableNames:        names,
		})
	}
	snapshot.Variables, err = r.captureTemplateVariables(ctx, q, projectID)
	if err != nil {
		return snapshot, err
	}
	source, err := q.GetProjectArtifactRepository(ctx, projectID)
	if err == nil {
		snapshot.PackageRepository = &artifact.Repository{
			URLTemplate: source.UrlTemplate,
			AuthType:    source.AuthType,
			Username:    source.Username,
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return snapshot, err
	}
	return snapshot, nil
}

func (r *Repository) captureTemplateVariables(
	ctx context.Context,
	q *db.Queries,
	projectID int64,
) ([]ProjectTemplateVariable, error) {
	variables, err := q.ListVariablesByProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	definitions := []ProjectTemplateVariable{}
	for i, v := range variables {
		value, err := r.displayValue(v.Value, v.Secret)
		if err != nil {
			return nil, err
		}
		definition := ProjectTemplateVariable{
			Key:    strconv.Itoa(i + 1),
			Name:   v.Name,
			Secret: v.Secret != 0,
		}
		if value.Valid {
			definition.Default = &value.String
		}
		if v.EnvironmentID.Valid {
			env, err := q.GetEnvironment(ctx, v.EnvironmentID.Int64)
			if err != nil {
				return nil, err
			}
			definition.EnvironmentID, definition.EnvironmentName = env.ID, env.Name
		}
		definitions = append(definitions, definition)
	}
	return definitions, nil
}

func (r *Repository) SaveProjectTemplate(
	ctx context.Context,
	actor ProjectTemplateActor,
	projectID int64,
	name, description string,
) (db.ProjectTemplate, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 255 {
		return db.ProjectTemplate{}, templateInvalid(
			"template name must contain 1 to 255 characters",
		)
	}
	var created db.ProjectTemplate
	err := r.templateTx(ctx, func(q *db.Queries) error {
		if _, err := templateAuthor(ctx, q, actor, projectID); err != nil {
			return err
		}
		snapshot, err := r.captureProjectTemplate(ctx, q, projectID)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(snapshot)
		if err != nil {
			return err
		}
		value, err := r.encryptValue(
			sql.NullString{String: string(encoded), Valid: true},
		)
		if err != nil {
			return err
		}
		created, err = q.CreateProjectTemplate(
			ctx,
			db.CreateProjectTemplateParams{
				SourceProjectID: projectID,
				CreatedBy: sql.NullInt64{
					Int64: actor.UserID,
					Valid: true,
				},
				Name:         name,
				Description:  description,
				SnapshotJson: value.String,
			},
		)
		return err
	})
	return created, err
}

func (r *Repository) ReadProjectTemplate(
	row db.ProjectTemplate,
) (ProjectTemplateSnapshot, error) {
	var snapshot ProjectTemplateSnapshot
	value, err := r.decryptValue(
		sql.NullString{String: row.SnapshotJson, Valid: true},
	)
	if err != nil {
		return snapshot, err
	}
	err = json.Unmarshal([]byte(value.String), &snapshot)
	return snapshot, err
}

func (r *Repository) CreateProjectFromTemplate(
	ctx context.Context,
	actor ProjectTemplateActor,
	sourceID, templateID int64,
	input ApplyProjectTemplate,
) (db.Project, error) {
	if actor.Scoped {
		return db.Project{}, ErrProjectTemplateAccess
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || utf8.RuneCountInString(input.Name) > 255 {
		return db.Project{}, templateInvalid(
			"project name must contain 1 to 255 characters",
		)
	}
	if !input.Reviewed {
		return db.Project{}, templateInvalid(
			"review the copied defaults, scripts, selectors, and references",
		)
	}
	var created db.Project
	err := r.templateTx(ctx, func(q *db.Queries) error {
		admin, err := templateAuthor(ctx, q, actor, sourceID)
		if err != nil {
			return err
		}
		if input.LifecycleID < 0 {
			return templateInvalid("invalid lifecycle")
		}
		if input.LifecycleID > 0 && !admin {
			return ErrProjectTemplateAccess
		}
		row, err := q.GetProjectTemplate(
			ctx,
			db.GetProjectTemplateParams{
				SourceProjectID: sourceID,
				ID:              templateID,
			},
		)
		if err != nil {
			return err
		}
		snapshot, err := r.ReadProjectTemplate(row)
		if err != nil {
			return err
		}
		if err := validateTemplateInputs(snapshot, input); err != nil {
			return err
		}
		description := snapshot.Description
		if input.Description != nil {
			description = *input.Description
		}
		created, err = q.CreateProject(
			ctx,
			db.CreateProjectParams{
				Name: input.Name,
				Description: sql.NullString{
					String: description,
					Valid:  description != "",
				},
			},
		)
		if err != nil {
			return err
		}
		if err := setTemplateLifecycle(
			ctx,
			q,
			created.ID,
			input.LifecycleID,
		); err != nil {
			return err
		}
		if err := q.AddProjectMember(
			ctx,
			db.AddProjectMemberParams{
				ProjectID: created.ID,
				UserID:    actor.UserID,
				Role:      "admin",
			},
		); err != nil {
			return err
		}
		if err := r.copyTemplateVariables(
			ctx,
			q,
			created.ID,
			snapshot,
			input,
		); err != nil {
			return err
		}
		if err := copyTemplateSteps(
			ctx,
			q,
			created.ID,
			snapshot,
			input,
		); err != nil {
			return err
		}
		if err := r.copyTemplatePackage(
			ctx,
			q,
			created.ID,
			snapshot,
			input,
		); err != nil {
			return err
		}
		created, err = q.GetProject(ctx, created.ID)
		return err
	})
	return created, err
}

func setTemplateLifecycle(
	ctx context.Context,
	q *db.Queries,
	projectID, lifecycleID int64,
) error {
	if lifecycleID == 0 {
		return nil
	}
	if _, err := q.GetLifecycle(ctx, lifecycleID); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		return templateInvalid("select an existing lifecycle")
	}
	return q.SetProjectLifecycle(ctx, db.SetProjectLifecycleParams{
		ID:          projectID,
		LifecycleID: sql.NullInt64{Int64: lifecycleID, Valid: true},
	})
}
