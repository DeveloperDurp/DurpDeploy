// Package mssqldriver translates the application's SQLite-flavored queries
// into the small subset of T-SQL required by the generated queries.
package mssqldriver

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// DriverName is registered with database/sql; open SQL Server connections
// through it (instead of "sqlserver") to get application-query rewriting.
const DriverName = "sqlserver-qmark"

var (
	ErrMalformedReturning = errors.New(
		"mssqldriver: malformed RETURNING clause",
	)
	ErrUnsupportedConflict = errors.New(
		"mssqldriver: unsupported conflict target",
	)

	limitRe = regexp.MustCompile(
		`(?is)\s+LIMIT\s+(@p\d+|\d+)(?:\s+OFFSET\s+(@p\d+|\d+))?(\s*(?:\)\s*)?;?\s*)$`,
	)
	conflictRe = regexp.MustCompile(
		`(?is)^(.*?)(INSERT INTO project_members \(project_id, user_id, role\) VALUES \((@p\d+), (@p\d+), (@p\d+)\))\s+ON CONFLICT \(project_id, user_id\) DO UPDATE SET role = excluded\.role(\s*;?)$`,
	)
	environmentPolicyConflictRe = regexp.MustCompile(
		`(?is)^(.*?)(INSERT INTO environment_agent_policies \(environment_id, pool_id, selector\)\s+VALUES \((@p\d+), (@p\d+), (@p\d+)\))\s+ON CONFLICT \(environment_id\) DO UPDATE SET\s+pool_id = excluded\.pool_id,\s+selector = excluded\.selector(\s*;?)$`,
	)
	projectMemberExistsRe = regexp.MustCompile(
		`(?is)^(\s*(?:--[^\r\n]*(?:\r?\n|$))?\s*)SELECT\s+EXISTS\s*\(\s*(SELECT\s+1\s+FROM\s+project_members\s+WHERE\s+project_id\s*=\s*(@p\d+)\s+AND\s+user_id\s*=\s*(@p\d+))\s*\)(\s*;?\s*)$`,
	)
)

const unixEpoch = "DATEDIFF_BIG(SECOND, '1970-01-01T00:00:00Z', SYSUTCDATETIME())"

// RewriteSQL converts one application SQL statement to T-SQL. It intentionally
// accepts only source query shapes used by the application rather than acting as
// a general SQL dialect translator.
func RewriteSQL(query string) (string, error) {
	query = rewriteTimes(query)
	query = rewritePlaceholders(query)
	query = replaceOutsideQuotes(query, " AS TEXT", " AS NVARCHAR(MAX)")

	if containsOutsideQuotes(query, "ON CONFLICT") {
		if rewritten, ok := rewriteRemoteAgentConflict(query); ok {
			query = rewritten
		} else {
			var err error
			query, err = rewriteConflict(query)
			if err != nil {
				return "", err
			}
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
	query = rewriteAgentClaimSelectors(query)
	return rewriteLimit(query), nil
}

func rewriteTimes(query string) string {
	var result strings.Builder
	result.Grow(len(query))
	for i := 0; i < len(query); {
		if quoteEnd, ok := quotedEnd(query, i); ok {
			result.WriteString(query[i:quoteEnd])
			i = quoteEnd
			continue
		}
		switch {
		case strings.HasPrefix(query[i:], "unixepoch()"):
			result.WriteString(unixEpoch)
			i += len("unixepoch()")
		case strings.HasPrefix(query[i:], "strftime('%s','now','start of day')"):
			result.WriteString(
				"DATEDIFF_BIG(SECOND, '1970-01-01T00:00:00Z', CONVERT(datetime2, CONVERT(date, SYSUTCDATETIME())))",
			)
			i += len("strftime('%s','now','start of day')")
		case strings.HasPrefix(query[i:], "strftime('%s','now')"):
			result.WriteString(unixEpoch)
			i += len("strftime('%s','now')")
		default:
			result.WriteByte(query[i])
			i++
		}
	}
	return result.String()
}

func quotedEnd(query string, start int) (int, bool) {
	if query[start] != '\'' && query[start] != '"' {
		return 0, false
	}
	quote := query[start]
	for end := start + 1; end < len(query); end++ {
		if query[end] != quote {
			continue
		}
		if end+1 < len(query) && query[end+1] == quote {
			end++
			continue
		}
		return end + 1, true
	}
	return len(query), true
}

func containsOutsideQuotes(query, keyword string) bool {
	return keywordIndexOutsideQuotes(query, keyword) >= 0
}

func keywordIndexOutsideQuotes(query, keyword string) int {
	upper := strings.ToUpper(query)
	keyword = strings.ToUpper(keyword)
	for i := 0; i < len(query); {
		if quoteEnd, ok := quotedEnd(query, i); ok {
			i = quoteEnd
			continue
		}
		if strings.HasPrefix(upper[i:], keyword) &&
			(i == 0 || !isSQLIdentifier(query[i-1])) &&
			(i+len(keyword) == len(query) || !isSQLIdentifier(query[i+len(keyword)])) {
			return i
		}
		i++
	}
	return -1

}

func isSQLIdentifier(value byte) bool {
	return value == '_' || value >= '0' && value <= '9' ||
		value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

func rewriteReturning(query string) (string, error) {
	start := keywordIndexOutsideQuotes(query, "RETURNING")
	if start < 0 {
		return "", ErrMalformedReturning
	}
	rest := strings.TrimLeft(query[start+len("RETURNING"):], " \t\r\n")
	if rest == "" {
		return "", ErrMalformedReturning
	}
	columnsEnd := 0
	for columnsEnd < len(rest) {
		if quoteEnd, ok := quotedEnd(rest, columnsEnd); ok {
			columnsEnd = quoteEnd
			continue
		}
		if rest[columnsEnd] == ';' {
			break
		}
		columnsEnd++
	}
	returningColumns := strings.TrimRight(rest[:columnsEnd], " \t\r\n")
	if strings.TrimSpace(returningColumns) == "" {
		return "", ErrMalformedReturning
	}
	output, err := outputColumns(returningColumns)
	if err != nil {
		return "", err
	}
	statement := strings.TrimRight(query[:start], " \t\r\n")
	trailing := rest[len(returningColumns):columnsEnd] + rest[columnsEnd:]
	switch {
	case containsOutsideQuotes(statement, "INSERT INTO"):
		values := keywordIndexOutsideQuotes(statement, "VALUES")
		if values < 0 {
			return "", ErrMalformedReturning
		}
		insertAt := values
		for insertAt > 0 && strings.ContainsRune(" \t\r\n", rune(statement[insertAt-1])) {
			insertAt--
		}
		return statement[:insertAt] + " OUTPUT " + output + statement[insertAt:] + trailing, nil
	case containsOutsideQuotes(statement, "UPDATE"):
		whereIndex := keywordIndexOutsideQuotes(statement, "WHERE")
		if whereIndex < 0 {
			return "", ErrMalformedReturning
		}
		start := whereIndex
		for start > 0 && strings.ContainsRune(
			" \t\r\n", rune(statement[start-1]),
		) {
			start--
		}
		separator := statement[start:whereIndex]
		return statement[:start] + " OUTPUT " + output + separator + statement[whereIndex:] + trailing, nil
	default:
		return "", ErrMalformedReturning
	}
}

func outputColumns(returning string) (string, error) {
	columns := strings.Split(strings.TrimSpace(returning), ",")
	if len(columns) == 0 {
		return "", ErrMalformedReturning
	}
	for i, column := range columns {
		column = strings.TrimSpace(column)
		if column == "" {
			return "", ErrMalformedReturning
		}
		if column == "*" {
			columns[i] = "INSERTED.*"
			continue
		}
		columns[i] = "INSERTED." + column
	}
	return strings.Join(columns, ", "), nil
}

func rewriteProjectMemberExists(query string) string {
	match := projectMemberExistsRe.FindStringSubmatch(query)
	if match == nil {
		return query
	}
	return match[1] + "SELECT CASE WHEN EXISTS(" + match[2] +
		") THEN CAST(1 AS BIGINT) ELSE CAST(0 AS BIGINT) END" + match[5]
}

func rewriteConflict(query string) (string, error) {
	match := conflictRe.FindStringSubmatch(query)
	if match != nil {
		return mergeProjectMembers(match), nil
	}
	match = environmentPolicyConflictRe.FindStringSubmatch(query)
	if match != nil {
		return mergeEnvironmentPolicy(match), nil
	}
	return "", fmt.Errorf("%w: unsupported target", ErrUnsupportedConflict)
}

func mergeProjectMembers(match []string) string {
	mergeSQL := match[1] + "MERGE project_members WITH (HOLDLOCK) AS target\n" +
		"USING (VALUES (" + match[3] + ", " + match[4] + ", " + match[5] + ")) AS source (project_id, user_id, role)\n" +
		"ON target.project_id = source.project_id AND target.user_id = source.user_id\n" +
		"WHEN MATCHED THEN UPDATE SET role = source.role\n" +
		"WHEN NOT MATCHED THEN INSERT (project_id, user_id, role)\n" +
		"VALUES (source.project_id, source.user_id, source.role);"
	if !strings.HasSuffix(mergeSQL, ";") {
		mergeSQL += ";"
	}
	return mergeSQL
}

func mergeEnvironmentPolicy(match []string) string {
	mergeSQL := match[1] + "MERGE environment_agent_policies WITH (HOLDLOCK) AS target\n" +
		"USING (VALUES (" + match[3] + ", " + match[4] + ", " + match[5] + ")) AS source (environment_id, pool_id, selector)\n" +
		"ON target.environment_id = source.environment_id\n" +
		"WHEN MATCHED THEN UPDATE SET pool_id = source.pool_id, selector = source.selector\n" +
		"WHEN NOT MATCHED THEN INSERT (environment_id, pool_id, selector)\n" +
		"VALUES (source.environment_id, source.pool_id, source.selector);"
	if !strings.HasSuffix(mergeSQL, ";") {
		mergeSQL += ";"
	}
	return mergeSQL
}

func rewriteLimit(query string) string {
	match := limitRe.FindStringSubmatchIndex(query)
	if match == nil {
		return query
	}
	limit := query[match[2]:match[3]]
	offset := ""
	if match[4] >= 0 {
		offset = query[match[4]:match[5]]
	}
	suffix := query[match[6]:match[7]]
	if offset != "" {
		limitStart := strings.Index(
			strings.ToUpper(query[match[0]:match[1]]),
			"LIMIT",
		)
		separator := query[match[0] : match[0]+limitStart]
		return query[:match[0]] + separator + "OFFSET " + offset + " ROWS FETCH NEXT " + limit + " ROWS ONLY" + suffix
	}
	selectIndex := keywordIndexOutsideQuotes(query[:match[0]], "SELECT")
	if selectIndex < 0 {
		return query
	}
	insertAt := selectIndex + len("SELECT")
	return query[:insertAt] + " TOP (" + limit + ")" + query[insertAt:match[0]] + suffix
}
