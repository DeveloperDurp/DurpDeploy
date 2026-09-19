package runner

import (
	"fmt"
	"sort"
	"strings"

	"durpdeploy/internal/db"
)

type ResolvedVariable struct {
	Name   string
	Value  string
	Secret bool
}

func ResolveReleaseVariables(variables []db.ReleaseVariable, environmentID int64) ([]ResolvedVariable, error) {
	ordered := append([]db.ReleaseVariable(nil), variables...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })

	global := make(map[string]ResolvedVariable)
	environment := make(map[string]ResolvedVariable)
	globalOrder := make([]string, 0, len(ordered))
	environmentOrder := make([]string, 0, len(ordered))
	for _, variable := range ordered {
		if strings.TrimSpace(variable.Name) == "" {
			return nil, fmt.Errorf("release variable name must not be empty")
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
	return result, nil
}
