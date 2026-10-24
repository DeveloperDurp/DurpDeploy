package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"durpdeploy/internal/db"
)

var ErrLibraryConflict = errors.New(
	"template conflicts with project configuration",
)

const (
	maxLibraryAgentSelectors = 32
	maxLibraryVariableBytes  = 16 * 1024
)

// swagger:model ApplyLibraryTemplate
type ApplyLibraryTemplate struct {
	Version        string            `json:"version"`
	Reviewed       bool              `json:"reviewed"`
	VariableValues map[string]string `json:"variable_values"`
	AgentSelectors []string          `json:"agent_selectors"`
}

// swagger:model LibraryApplyResult
type LibraryApplyResult struct {
	ProjectID        int64  `json:"project_id"`
	Slug             string `json:"slug"`
	Version          string `json:"version"`
	StepsCreated     int    `json:"steps_created"`
	VariablesCreated int    `json:"variables_created"`
}

func (r *Repository) ApplyLibraryTemplate(
	ctx context.Context,
	actor ProjectTemplateActor,
	projectID int64,
	slug string,
	input ApplyLibraryTemplate,
) (LibraryApplyResult, error) {
	entry, err := GetLibraryTemplate(slug)
	if err != nil {
		return LibraryApplyResult{}, err
	}
	if err := validateLibraryInput(entry, input); err != nil {
		return LibraryApplyResult{}, err
	}
	err = withSQLiteBusyRetry(ctx, func() error {
		return r.WithTx(ctx, func(queries *db.Queries) error {
			// Lock before reads: concurrent applications must see committed
			// variables and append after the previous operation on every backend.
			locked, err := queries.LockProject(ctx, projectID)
			if err != nil {
				return err
			}
			if locked != 1 {
				return sql.ErrNoRows
			}
			if _, err := templateAuthor(ctx, queries, actor, projectID); err != nil {
				return err
			}
			return r.copyLibraryTemplate(ctx, queries, projectID, entry, input)
		})
	})
	if err != nil {
		return LibraryApplyResult{}, err
	}
	return LibraryApplyResult{
		ProjectID:        projectID,
		Slug:             entry.Slug,
		Version:          entry.Version,
		StepsCreated:     len(entry.Snapshot.Steps),
		VariablesCreated: len(entry.Snapshot.Variables),
	}, nil
}

func (r *Repository) copyLibraryTemplate(
	ctx context.Context,
	queries *db.Queries,
	projectID int64,
	entry LibraryTemplate,
	input ApplyLibraryTemplate,
) error {
	if err := checkLibraryConflicts(ctx, queries, projectID, entry); err != nil {
		return err
	}
	labels, err := queries.ListAvailableAgentLabels(ctx)
	if err != nil {
		return err
	}
	selectors, err := templateAgentSelectors(input.AgentSelectors, labels)
	if err != nil {
		return err
	}
	if LibraryUsesAgents(entry) && len(selectors) == 0 {
		return templateInvalid("select at least one registered agent label")
	}
	steps, err := queries.ListStepsByProject(ctx, projectID)
	if err != nil {
		return err
	}
	nextSortOrder := int64(1)
	for _, step := range steps {
		if step.SortOrder > math.MaxInt64-int64(len(entry.Snapshot.Steps)) {
			return templateInvalid("step order exceeds the supported range")
		}
		nextSortOrder = max(nextSortOrder, step.SortOrder+1)
	}
	snapshot, copyInput := prepareLibraryCopy(
		entry,
		input,
		selectors,
		nextSortOrder,
	)
	for _, variable := range snapshot.Variables {
		if variable.Secret && r.secrets == nil {
			return templateInvalid("secret variables require encryption")
		}
	}
	if err := r.copyTemplateVariables(ctx, queries, projectID, snapshot, copyInput); err != nil {
		return err
	}
	return copyTemplateSteps(ctx, queries, projectID, snapshot, copyInput)
}

func prepareLibraryCopy(
	entry LibraryTemplate, input ApplyLibraryTemplate,
	selectors []string, nextSortOrder int64,
) (ProjectTemplateSnapshot, ApplyProjectTemplate) {
	snapshot := entry.Snapshot
	copyInput := ApplyProjectTemplate{
		VariableValues: input.VariableValues,
		AgentSelectors: map[string][]string{},
	}
	for stepIndex := range snapshot.Steps {
		snapshot.Steps[stepIndex].SortOrder = nextSortOrder + int64(stepIndex)
		if entry.Slug == "terraform-plan-apply" {
			snapshot.Steps[stepIndex].ContainerImage = input.VariableValues["TF_CONTAINER_IMAGE"]
		}
		// The shared copy helper uses one-based step positions as keys.
		stepKey := strconv.Itoa(stepIndex + 1)
		if snapshot.Steps[stepIndex].ExecutionTarget == "agent" {
			copyInput.AgentSelectors[stepKey] = selectors
		}
	}
	return snapshot, copyInput
}

func checkLibraryConflicts(
	ctx context.Context,
	queries *db.Queries,
	projectID int64,
	entry LibraryTemplate,
) error {
	variables, err := queries.ListVariablesByProject(ctx, projectID)
	if err != nil {
		return err
	}
	existingNames := map[string]bool{}
	for _, variable := range variables {
		existingNames[variable.Name] = true
	}
	project, err := queries.GetProject(ctx, projectID)
	if err != nil {
		return err
	}
	if project.LifecycleID.Valid {
		shared, err := queries.ListLifecycleVariables(
			ctx,
			project.LifecycleID.Int64,
		)
		if err != nil {
			return err
		}
		for _, variable := range shared {
			existingNames[variable.Name] = true
		}
	}
	for _, variable := range entry.Snapshot.Variables {
		if existingNames[variable.Name] {
			return fmt.Errorf(
				"%w: variable %s already exists in the project or lifecycle; no changes were made",
				ErrLibraryConflict,
				variable.Name,
			)
		}
	}
	return nil
}

func validateLibraryInput(
	entry LibraryTemplate,
	input ApplyLibraryTemplate,
) error {
	if input.Version != entry.Version {
		return fmt.Errorf(
			"%w: preview the current template version",
			ErrLibraryConflict,
		)
	}
	if !input.Reviewed {
		return templateInvalid(
			"review scripts, configuration, dependencies, and permissions before applying",
		)
	}
	if (LibraryUsesAgents(entry) && len(input.AgentSelectors) == 0) ||
		len(input.AgentSelectors) > maxLibraryAgentSelectors {
		return templateInvalid("select between 1 and 32 agent labels")
	}
	if !LibraryUsesAgents(entry) && len(input.AgentSelectors) != 0 {
		return templateInvalid(
			"this template uses server containers, not agent labels",
		)
	}
	for name := range input.VariableValues {
		if _, ok := entry.Configuration[name]; !ok {
			return templateInvalid("unknown template variable")
		}
	}
	for name, config := range entry.Configuration {
		if err := validateLibraryVariable(name, config, input.VariableValues[name]); err != nil {
			return err
		}
	}
	return nil
}

func validateLibraryVariable(
	name string,
	config LibraryVariableConfig,
	value string,
) error {
	if config.Required && strings.TrimSpace(value) == "" {
		return templateInvalid("supply a value for " + name)
	}
	if len(value) > maxLibraryVariableBytes ||
		strings.ContainsAny(value, "\x00\r\n") {
		return templateInvalid("invalid value for " + name)
	}
	if value != "" && !validLibraryValue(config.Format, value) {
		return templateInvalid("invalid " + config.Format + " for " + name)
	}
	return nil
}

var (
	libraryBearer  = regexp.MustCompile(`^[a-zA-Z0-9._~+/-]*=*$`)
	libraryService = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_@.:-]*$`)
	libraryCompose = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
)

func validLibraryValue(format, value string) bool {
	switch format {
	case "https-url", "http-url":
		return validLibraryURL(format, value)
	case "path", "optional-path":
		return filepath.IsAbs(value) && filepath.Clean(value) != "/" &&
			!strings.HasSuffix(value, "/")
	case "bearer":
		return libraryBearer.MatchString(value)
	case "service":
		return libraryService.MatchString(value)
	case "compose-project":
		return libraryCompose.MatchString(value)
	case "image":
		return !strings.HasPrefix(value, "-") &&
			!strings.ContainsAny(value, " \t")
	case "text":
		return true // Length and control characters are checked by the caller.
	default:
		return false
	}
}

func validLibraryURL(format, value string) bool {
	parsed, err := url.Parse(value)
	if err != nil {
		return false
	}
	if parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return false
	}
	switch parsed.Scheme {
	case "https":
		return true
	case "http":
		return format == "http-url"
	default:
		return false
	}
}
