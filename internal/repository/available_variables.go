package repository

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	"durpdeploy/internal/db"
)

// VariableDefinition is display data. Secret values are never decrypted.
// swagger:model VariableDefinition
type VariableDefinition struct {
	Key        string `json:"key"`
	Name       string `json:"name"`
	Source     string `json:"source"`
	SourceName string `json:"source_name"`
	// Null for unscoped defaults and runtime variables.
	// Extensions:
	// ---
	// x-nullable: true
	EnvironmentID *int64 `json:"environment_id"`
	Scope         string `json:"scope"`
	Secret        bool   `json:"secret"`
	// Null for secrets and runtime values assigned at execution.
	// Extensions:
	// ---
	// x-nullable: true
	Value        *string `json:"value"`
	Description  string  `json:"description"`
	Effective    bool    `json:"effective"`
	Status       string  `json:"status"`
	Reason       string  `json:"reason"`
	OverriddenBy string  `json:"overridden_by,omitempty"`
	Reference    string  `json:"reference"`
}

// VariableEnvironment deliberately contains no environment configuration.
// swagger:model VariableEnvironment
type VariableEnvironment struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// AvailableVariables describes current authoring definitions, not snapshots.
// swagger:model AvailableVariables
type AvailableVariables struct {
	Configured   []VariableDefinition  `json:"configured"`
	Predefined   []VariableDefinition  `json:"predefined"`
	Environments []VariableEnvironment `json:"environments"`
	Context      VariableContext       `json:"context"`
	Warnings     []string              `json:"warnings"`
}

// VariableContext contains only draft metadata, never script text or values.
// swagger:model VariableContext
type VariableContext struct {
	EnvironmentID int64    `json:"environment_id"`
	Interpreter   string   `json:"interpreter"`
	Target        string   `json:"execution_target"`
	AgentMode     string   `json:"agent_execution_mode"`
	VariableNames []string `json:"variable_names"`
}

func (r *Repository) AvailableVariables(
	ctx context.Context, project db.Project, preview VariableContext,
) (AvailableVariables, error) {
	result := AvailableVariables{
		Configured: []VariableDefinition{}, Predefined: []VariableDefinition{},
		Environments: []VariableEnvironment{}, Context: preview,
		Warnings: []string{},
	}
	_, options, err := r.VariableEnvironments(ctx, project)
	if err != nil {
		return result, err
	}
	names := make(map[int64]string, len(options))
	for _, environment := range options {
		names[environment.ID] = environment.Name
		result.Environments = append(result.Environments, VariableEnvironment{
			ID: environment.ID, Name: environment.Name,
		})
	}
	local, err := r.ListProjectVariablesForDisplay(ctx, project.ID)
	if err != nil {
		return result, err
	}
	// Match snapshot order, including the last-ID rule for legacy duplicates.
	sort.SliceStable(
		local,
		func(i, j int) bool { return local[i].ID < local[j].ID },
	)
	inherited, err := r.inheritedVariables(ctx, project, local, true)
	if err != nil {
		return result, err
	}
	for _, variable := range inherited.Variables {
		result.Configured = append(result.Configured, variableDefinition(
			VariableDefinition{
				Key: fmt.Sprintf(
					"lifecycle:%d",
					variable.ID,
				), Name: variable.Name,
				Source: "lifecycle", SourceName: inherited.Lifecycle.Name,
				Secret: variable.Secret != 0,
			}, variable.EnvironmentID, variable.Value, names,
		))
	}
	for _, variable := range local {
		result.Configured = append(result.Configured, variableDefinition(
			VariableDefinition{
				Key: fmt.Sprintf(
					"project:%d",
					variable.ID,
				), Name: variable.Name,
				Source: "project", SourceName: project.Name,
				Secret: variable.Secret != 0,
			}, variable.EnvironmentID, variable.Value, names,
		))
	}
	result.resolveConfigured()
	result.addPredefined()
	return result, nil
}

func variableDefinition(
	v VariableDefinition, environment sql.NullInt64,
	value sql.NullString, environments map[int64]string,
) VariableDefinition {
	v.Scope = "Unscoped default"
	if !v.Secret {
		v.Value = &value.String
	}
	if environment.Valid {
		v.EnvironmentID = &environment.Int64
		v.Scope = fmt.Sprintf("Environment #%d", environment.Int64)
		if name := environments[environment.Int64]; name != "" {
			v.Scope = name
		}
	}
	return v
}

func (result *AvailableVariables) resolveConfigured() {
	// Project definitions follow lifecycle definitions and win in the same scope.
	scopes := make(map[variableScope]int)
	for i, v := range result.Configured {
		scopes[v.scopeKey()] = i
	}
	for i := range result.Configured {
		v := &result.Configured[i]
		v.Reference = variableReference(v.Name, result.Context.Interpreter)
		winner := result.configuredWinner(*v, scopes)
		switch {
		case winner != i:
			v.Status, v.Reason = "overridden", "Another definition takes precedence."
			v.OverriddenBy = result.Configured[winner].Key
		case result.Context.EnvironmentID == 0:
			v.Status = "requires_environment"
			v.Reason = "Select an environment to determine effective values."
		case v.EnvironmentID != nil && *v.EnvironmentID != result.Context.EnvironmentID:
			v.Status, v.Reason = "out_of_scope", "Configured for another environment."
		default:
			v.Effective = true
			v.Status, v.Reason = result.Context.configuredAvailability(v.Name)
		}
	}
	result.warnMissingSelections()
	if result.Context.EnvironmentID == 0 {
		result.Warnings = append(result.Warnings,
			"Unscoped defaults can be replaced by environment-specific values.")
	}
	result.Warnings = append(
		result.Warnings,
		"Current project and lifecycle definitions are shown. Existing release and deployment snapshots are unchanged.",
	)
}

func (v VariableDefinition) scopeKey() variableScope {
	scope := variableScope{name: v.Name}
	if v.EnvironmentID != nil {
		scope.environment = sql.NullInt64{Int64: *v.EnvironmentID, Valid: true}
	}
	return scope
}

func (result *AvailableVariables) configuredWinner(
	v VariableDefinition, scopes map[variableScope]int,
) int {
	if result.Context.EnvironmentID != 0 && v.EnvironmentID == nil {
		envScope := variableScope{name: v.Name, environment: sql.NullInt64{
			Int64: result.Context.EnvironmentID, Valid: true,
		}}
		if index, exists := scopes[envScope]; exists {
			return index
		}
	}
	return scopes[v.scopeKey()]
}

func (result *AvailableVariables) warnMissingSelections() {
	for _, name := range result.Context.VariableNames {
		found := false
		for _, variable := range result.Configured {
			if variable.Name == name && (variable.Effective ||
				result.Context.EnvironmentID == 0) {
				found = true
			}
		}
		if !found {
			result.Warnings = append(
				result.Warnings,
				fmt.Sprintf(
					"Selected variable %s has no value in this environment.",
					name,
				),
			)
		}
	}
}
