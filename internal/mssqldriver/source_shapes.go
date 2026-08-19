package mssqldriver

import "strings"

func rewriteLatestDeploymentDerivedTable(query string) string {
	if !containsOutsideQuotes(
		query,
		"PARTITION BY d.release_id, d.environment_id",
	) {
		return query
	}
	return replaceOutsideQuotes(
		query,
		") WHERE rn = 1",
		") AS latest_deployment WHERE rn = 1",
	)
}

func rewriteDeploymentFilterIntegerCasts(query string) string {
	if !containsOutsideQuotes(query, "FROM deployments d") &&
		!containsOutsideQuotes(query, "FROM audit_log") {
		return query
	}
	return replaceOutsideQuotes(query, " AS INTEGER", " AS BIGINT")
}

func rewriteAgentClaimSelectors(query string) string {
	if !containsOutsideQuotes(query, "instr(") {
		return query
	}
	query = replaceOutsideQuotes(query, " || ", " + ")
	query = replaceOutsideQuotes(query, "length(", "LEN(")
	query = replaceOutsideQuotes(query, `instr(
                ',' + deployment_dispatches.selector + ',',
                ',' + t.tag_key + '=' + t.tag_value + ','
            )`, `CHARINDEX(
                ',' + t.tag_key + '=' + t.tag_value + ',',
                ',' + deployment_dispatches.selector + ','
            )`)
	return replaceOutsideQuotes(query, `instr(
                    ',' + candidate.selector + ',',
                    ',' + tag.tag_key + '=' + tag.tag_value + ','
                )`, `CHARINDEX(
                    ',' + tag.tag_key + '=' + tag.tag_value + ',',
                    ',' + candidate.selector + ','
                )`)
}

func replaceOutsideQuotes(query, old, replacement string) string {
	var result strings.Builder
	result.Grow(len(query))
	for i := 0; i < len(query); {
		if quoteEnd, ok := quotedEnd(query, i); ok {
			result.WriteString(query[i:quoteEnd])
			i = quoteEnd
			continue
		}
		if strings.HasPrefix(query[i:], old) {
			result.WriteString(replacement)
			i += len(old)
			continue
		}
		result.WriteByte(query[i])
		i++
	}
	return result.String()
}
