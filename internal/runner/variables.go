package runner

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"durpdeploy/internal/containerenv"
	"durpdeploy/internal/db"
)

type ResolvedVariable struct {
	Name   string
	Value  string
	Secret bool
}

// ResolveDeploymentVariables uses identity captured at deployment acceptance.
// Legacy user definitions cannot override or mark runtime identity as secret.
func ResolveDeploymentVariables(
	variables []db.ReleaseVariable,
	deployment db.Deployment,
) ([]ResolvedVariable, error) {
	if !deployment.EnvironmentNameSnapshot.Valid {
		return nil, fmt.Errorf(
			"%w: missing accepted environment identity",
			containerenv.ErrEnvironment,
		)
	}
	resolved, err := ResolveReleaseVariables(
		variables,
		deployment.EnvironmentID,
	)
	if err != nil {
		return nil, err
	}
	resolved = slices.DeleteFunc(resolved, func(v ResolvedVariable) bool {
		return containerenv.IsEnvironmentIdentity(v.Name)
	})
	return append(
		resolved,
		ResolvedVariable{
			Name:  containerenv.EnvironmentNameVariable,
			Value: deployment.EnvironmentNameSnapshot.String,
		},
		ResolvedVariable{
			Name:  containerenv.EnvironmentIDVariable,
			Value: strconv.FormatInt(deployment.EnvironmentID, 10),
		},
	), nil
}

func ResolveReleaseVariables(
	variables []db.ReleaseVariable,
	environmentID int64,
) ([]ResolvedVariable, error) {
	ordered := append([]db.ReleaseVariable(nil), variables...)
	sort.SliceStable(
		ordered,
		func(i, j int) bool { return ordered[i].ID < ordered[j].ID },
	)

	global := make(map[string]ResolvedVariable)
	environment := make(map[string]ResolvedVariable)
	globalOrder := make([]string, 0, len(ordered))
	environmentOrder := make([]string, 0, len(ordered))
	for _, variable := range ordered {
		if strings.TrimSpace(variable.Name) == "" {
			return nil, fmt.Errorf(
				"%w: release variable name must not be empty",
				containerenv.ErrEnvironment,
			)
		}
		resolved := ResolvedVariable{
			Name: variable.Name, Value: variable.Value.String, Secret: variable.Secret != 0,
		}
		if variable.EnvironmentID.Valid {
			if variable.EnvironmentID.Int64 != environmentID {
				continue
			}
			if _, exists := environment[variable.Name]; !exists {
				environmentOrder = append(environmentOrder, variable.Name)
			}
			environment[variable.Name] = resolved
			continue
		}
		if _, exists := global[variable.Name]; !exists {
			globalOrder = append(globalOrder, variable.Name)
		}
		global[variable.Name] = resolved
	}

	return resolvedVariablesInOrder(
		global,
		environment,
		globalOrder,
		environmentOrder,
	), nil
}

func resolvedVariablesInOrder(
	global, environment map[string]ResolvedVariable,
	globalOrder, environmentOrder []string,
) []ResolvedVariable {
	result := make([]ResolvedVariable, 0, len(global)+len(environment))
	seen := make(map[string]struct{}, len(global)+len(environment))
	for _, name := range globalOrder {
		if value, overridden := environment[name]; overridden {
			result = append(result, value)
		} else {
			result = append(result, global[name])
		}
		seen[name] = struct{}{}
	}
	for _, name := range environmentOrder {
		if _, exists := seen[name]; exists {
			continue
		}
		result = append(result, environment[name])
	}
	return result
}
