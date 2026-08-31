package runner

import "durpdeploy/internal/db"

// ResolveReleaseVariables returns one variable per name for an environment.
// Release variable IDs preserve the immutable snapshot insertion order, so the
// last row in each scope wins; environment-scoped rows then override globals.
func ResolveReleaseVariables(
	variables []db.ReleaseVariable,
	environmentID int64,
) []db.ReleaseVariable {
	resolved := make([]db.ReleaseVariable, 0, len(variables))
	indexes := make(map[string]int, len(variables))
	for _, environmentScoped := range []bool{false, true} {
		for _, variable := range variables {
			if variable.EnvironmentID.Valid != environmentScoped {
				continue
			}
			if environmentScoped &&
				variable.EnvironmentID.Int64 != environmentID {
				continue
			}
			if index, found := indexes[variable.Name]; found {
				resolved[index] = variable
				continue
			}
			indexes[variable.Name] = len(resolved)
			resolved = append(resolved, variable)
		}
	}
	return resolved
}
