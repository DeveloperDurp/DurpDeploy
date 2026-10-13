package mssqldriver

import "strings"

// RewriteSQL converts application queries to the supported T-SQL shapes.
func RewriteSQL(query string) (string, error) {
	// Bound preview reads before the driver allocates NVARCHAR payloads.
	if strings.HasPrefix(query, "-- name: ListDeploymentLogsBefore") ||
		strings.HasPrefix(query, "-- name: ListDeploymentLogPreviewsAfter") {
		for _, column := range []string{"line", "step_name", "step_state"} {
			query = strings.ReplaceAll(query,
				"substr(CAST("+column+" AS BLOB), 1,",
				"SUBSTRING("+column+", 1,")
		}
	}
	// Polling skips claims without waiting while holding the waiting index lock.
	if strings.Contains(query, "-- name: ListWaitingRemoteDeploymentClaims") ||
		strings.Contains(query, "-- name: ListWaitingRemoteStepRuns") {
		const hint = " WITH (READPAST, READCOMMITTEDLOCK)"
		query = strings.ReplaceAll(query, "remote_deployment_claims c\n",
			"remote_deployment_claims c"+hint+"\n")
		query = strings.ReplaceAll(query, "remote_step_runs r\n",
			"remote_step_runs r"+hint+"\n")
		query = strings.ReplaceAll(query, "JOIN deployments d ON",
			"JOIN deployments d"+hint+" ON")
	}
	query = rewriteTimes(query)
	query = rewritePlaceholders(query)
	query = replaceOutsideQuotes(query, " AS TEXT", " AS NVARCHAR(MAX)")
	if containsOutsideQuotes(query, "ON CONFLICT") {
		var err error
		query, err = rewriteProjectMembersConflict(query)
		if err != nil {
			return "", err
		}
	}
	if containsOutsideQuotes(query, "RETURNING") {
		var err error
		query, err = rewriteReturning(query)
		if err != nil {
			return "", err
		}
	}
	query = rewriteProjectMemberExists(query)
	query = rewriteLatestDeploymentDerivedTable(query)
	query = rewriteDeploymentFilterIntegerCasts(query)
	return rewriteLimit(query), nil
}
