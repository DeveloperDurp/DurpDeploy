package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"durpdeploy/internal/agentexecution"
	"durpdeploy/internal/artifact"
	"durpdeploy/internal/containerenv"
	"durpdeploy/internal/db"
	"durpdeploy/internal/interpreter"
)

func validateTemplateInputs(
	snapshot ProjectTemplateSnapshot,
	input ApplyProjectTemplate,
) error {
	keys, environments := map[string]bool{}, map[string]bool{}
	for _, v := range snapshot.Variables {
		keys[v.Key] = true
		if v.EnvironmentID != 0 {
			environments[strconv.FormatInt(v.EnvironmentID, 10)] = true
		}
		if v.Secret && input.VariableValues[v.Key] == "" {
			return templateInvalid("supply a new value for secret " + v.Name)
		}
	}
	for key := range input.VariableValues {
		if !keys[key] {
			return templateInvalid("unknown variable key")
		}
	}
	if err := validateTemplateEnvironmentMappings(
		environments,
		input.EnvironmentMappings,
	); err != nil {
		return err
	}
	for key := range input.AgentSelectors {
		if err := validateTemplateSelectorKey(key, snapshot.Steps); err != nil {
			return err
		}
	}
	if snapshot.PackageRepository != nil && input.PackageRepository == nil {
		return templateInvalid(
			"supply a package repository and fresh credentials",
		)
	}
	return nil
}

func validateTemplateEnvironmentMappings(
	environments map[string]bool,
	mappings map[string]int64,
) error {
	for key, target := range mappings {
		if !environments[key] || target <= 0 {
			return templateInvalid("invalid environment mapping")
		}
	}
	for key := range environments {
		if mappings[key] <= 0 {
			return templateInvalid("map every environment-scoped variable")
		}
	}
	return nil
}

func validateTemplateSelectorKey(
	key string,
	steps []ProjectTemplateStep,
) error {
	i, err := strconv.Atoi(key)
	if err != nil || i < 1 || i > len(steps) || strconv.Itoa(i) != key ||
		steps[i-1].ExecutionTarget != "agent" {
		return templateInvalid("invalid agent selector step")
	}
	return nil
}

func templateVariableEnvironment(
	ctx context.Context,
	q *db.Queries,
	variable ProjectTemplateVariable,
	input ApplyProjectTemplate,
	stageIDs []int64,
) (sql.NullInt64, error) {
	if variable.EnvironmentID == 0 {
		return sql.NullInt64{}, nil
	}
	id := input.EnvironmentMappings[strconv.FormatInt(variable.EnvironmentID, 10)]
	if _, err := q.GetEnvironment(ctx, id); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return sql.NullInt64{}, err
		}
		return sql.NullInt64{}, templateInvalid(
			"select an existing environment",
		)
	}
	if input.LifecycleID > 0 && !slices.Contains(stageIDs, id) {
		return sql.NullInt64{}, templateInvalid(
			"environment is not in the selected lifecycle",
		)
	}
	return sql.NullInt64{Int64: id, Valid: true}, nil
}

func (r *Repository) copyTemplateVariables(
	ctx context.Context,
	q *db.Queries,
	projectID int64,
	snapshot ProjectTemplateSnapshot,
	input ApplyProjectTemplate,
) error {
	stageIDs := []int64{}
	if input.LifecycleID > 0 {
		var err error
		stageIDs, err = q.ListLifecycleStageEnvironmentIDs(
			ctx,
			input.LifecycleID,
		)
		if err != nil {
			return err
		}
	}
	seen := map[string]bool{}
	for _, v := range snapshot.Variables {
		if err := validateVariableName(v.Name); err != nil {
			return templateInvalid("invalid variable name")
		}
		if err := containerenv.ValidateName(v.Name, false); err != nil {
			return templateInvalid(err.Error())
		}
		environment, err := templateVariableEnvironment(
			ctx,
			q,
			v,
			input,
			stageIDs,
		)
		if err != nil {
			return err
		}
		key := fmt.Sprintf("%s/%d", v.Name, environment.Int64)
		if seen[key] {
			return templateInvalid(
				"environment mappings create duplicate variable definitions",
			)
		}
		seen[key] = true
		var value sql.NullString
		if v.Default != nil && !v.Secret {
			value = sql.NullString{String: *v.Default, Valid: true}
		}
		if supplied, ok := input.VariableValues[v.Key]; ok {
			value = sql.NullString{String: supplied, Valid: true}
		}
		encrypted, err := r.encryptValue(value)
		if err != nil {
			return err
		}
		secret := int64(0)
		if v.Secret {
			secret = 1
		}
		if _, err := q.CreateVariable(
			ctx,
			db.CreateVariableParams{
				ProjectID:     projectID,
				Name:          v.Name,
				Value:         encrypted,
				EnvironmentID: environment,
				Secret:        secret,
			},
		); err != nil {
			return err
		}
	}
	return nil
}

func templateAgentSelectors(selectors, labels []string) ([]string, error) {
	canonical := []string{}
	for _, label := range selectors {
		label = strings.ToLower(strings.TrimSpace(label))
		if label == "" {
			continue
		}
		if !slices.Contains(labels, label) {
			return nil, templateInvalid(
				"select available agent labels or explicitly clear the selectors",
			)
		}
		if !slices.Contains(canonical, label) {
			canonical = append(canonical, label)
		}
	}
	return canonical, nil
}

func copyTemplateSteps(
	ctx context.Context,
	q *db.Queries,
	projectID int64,
	snapshot ProjectTemplateSnapshot,
	input ApplyProjectTemplate,
) error {
	labels, err := q.ListAvailableAgentLabels(ctx)
	if err != nil {
		return err
	}
	for i, s := range snapshot.Steps {
		if err := validateTemplateStep(s); err != nil {
			return err
		}
		selectors := s.AgentSelectors
		if supplied, ok := input.AgentSelectors[strconv.Itoa(i+1)]; ok {
			selectors = supplied
		}
		if s.ExecutionTarget == "agent" {
			selectors, err = templateAgentSelectors(selectors, labels)
			if err != nil {
				return err
			}
		}
		names, err := json.Marshal(s.VariableNames)
		if err != nil {
			return err
		}
		step, err := q.CreateStep(ctx, db.CreateStepParams{
			ProjectID:            projectID,
			Name:                 s.Name,
			ScriptBody:           s.ScriptBody,
			Interpreter:          s.Interpreter,
			SortOrder:            s.SortOrder,
			TimeoutSeconds:       s.TimeoutSeconds,
			MaxRetries:           s.MaxRetries,
			AgentExecutionMode:   s.AgentExecutionMode,
			ContainerImage:       s.ContainerImage,
			NetworkMode:          s.NetworkMode,
			ApprovalArtifactPath: s.ApprovalArtifactPath,
			ApprovalReviewPath:   s.ApprovalReviewPath,
			ApprovalReviewFormat: s.ApprovalReviewFormat,
			VariableNames:        string(names),
		})
		if err != nil {
			return err
		}
		if err := setStepPlacement(
			ctx,
			q,
			step.ID,
			s.ExecutionTarget,
			selectors,
		); err != nil {
			return err
		}
	}
	return nil
}

func validateTemplateStep(s ProjectTemplateStep) error {
	if strings.TrimSpace(s.Name) == "" ||
		(s.ExecutionTarget != "local" && s.ExecutionTarget != "agent") ||
		s.TimeoutSeconds < 0 ||
		s.MaxRetries < 0 {
		return templateInvalid("invalid step configuration")
	}
	if _, err := interpreter.Validate(s.Interpreter); err != nil {
		return templateInvalid(err.Error())
	}
	if _, err := agentexecution.Parse(
		agentexecution.Config{
			Target:        s.ExecutionTarget,
			Mode:          s.AgentExecutionMode,
			Image:         s.ContainerImage,
			VariableNames: s.VariableNames,
		},
	); err != nil {
		return templateInvalid(err.Error())
	}
	local := s.ExecutionTarget == "local" || s.AgentExecutionMode == "container"
	if local && s.ContainerImage == "" {
		return templateInvalid("container image is required")
	}
	seen := map[string]bool{}
	for _, name := range s.VariableNames {
		if err := containerenv.ValidateName(name, local); err != nil {
			return templateInvalid(err.Error())
		}
		if seen[name] {
			return templateInvalid("duplicate step variable restriction")
		}
		seen[name] = true
	}
	if err := artifact.ValidateGateConfig(
		s.ExecutionTarget,
		s.NetworkMode,
		s.ApprovalArtifactPath,
		s.ApprovalReviewPath,
		s.ApprovalReviewFormat,
	); err != nil {
		return templateInvalid(err.Error())
	}
	return nil
}

func (r *Repository) copyTemplatePackage(
	ctx context.Context,
	q *db.Queries,
	projectID int64,
	snapshot ProjectTemplateSnapshot,
	input ApplyProjectTemplate,
) error {
	if input.PackageRepository == nil {
		return nil
	}
	p := input.PackageRepository
	source := artifact.Repository{
		URLTemplate: p.URLTemplate,
		AuthType:    p.AuthType,
		Username:    p.Username,
		Credential:  p.Credential,
	}
	if err := source.Validate(); err != nil {
		return templateInvalid(
			"invalid package repository or missing credentials",
		)
	}
	if p.Credential != "" && r.secrets == nil {
		return templateInvalid("package credentials require secret encryption")
	}
	credential, err := r.encryptValue(
		sql.NullString{String: p.Credential, Valid: p.Credential != ""},
	)
	if err != nil {
		return err
	}
	row, err := writeProjectPackageSource(
		ctx,
		q,
		db.CreatePackageRepositoryParams{
			ProjectID:   projectID,
			UrlTemplate: p.URLTemplate,
			AuthType:    p.AuthType,
			Username:    p.Username,
			Credential:  credential.String,
		},
	)
	if err != nil {
		return err
	}
	return q.SelectProjectArtifactRepository(
		ctx,
		db.SelectProjectArtifactRepositoryParams{
			ProjectID:    projectID,
			RepositoryID: row.ID,
		},
	)
}
