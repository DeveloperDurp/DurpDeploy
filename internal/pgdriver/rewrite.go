// Package pgdriver lets the app run against PostgreSQL using the exact same
// migrations and sqlc-generated queries written for SQLite. Rather than
// maintaining a second copy of the schema/queries (see the P2 plan decision:
// one compatible SQL source until the need for engine-specific SQL actually
// arises), it registers a database/sql driver that rewrites each statement
// on the fly before handing it to pgx:
//
//   - the handful of SQLite-only constructs actually in use (AUTOINCREMENT
//     primary keys, plain INTEGER columns, unixepoch()/strftime()) are
//     translated to their PostgreSQL equivalent, and
//   - `?` positional placeholders are renumbered to `$1, $2, ...`.
//
// This runs for both migration DDL (goose) and application queries (sqlc),
// since both ultimately execute through the same *sql.DB/*sql.Tx.
package pgdriver

import (
	"regexp"
	"strconv"
	"strings"
)

// DriverName is registered with database/sql; open Postgres connections
// through it (instead of "pgx") to get the SQL rewriting below.
const DriverName = "pgx-qmark"

var (
	autoIncrementRe = regexp.MustCompile(
		`(?i)INTEGER\s+PRIMARY\s+KEY\s+AUTOINCREMENT`,
	)
	integerRe       = regexp.MustCompile(`\bINTEGER\b`)
	blobRe          = regexp.MustCompile(`\bBLOB\b`)
	unixepochRe     = regexp.MustCompile(`(?i)unixepoch\(\)`)
	strftimeTodayRe = regexp.MustCompile(
		`(?i)strftime\('%s',\s*'now',\s*'start of day'\)`,
	)
	instrRe = regexp.MustCompile(`(?i)\binstr\s*\(`)
)

// RewriteSQL translates a single SQLite-flavored SQL statement into
// PostgreSQL-compatible SQL, renumbering `?` placeholders in the process.
func RewriteSQL(query string) string {
	if strings.Contains(strings.ToUpper(query), "INTEGER") {
		query = autoIncrementRe.ReplaceAllString(query, "BIGSERIAL PRIMARY KEY")
		query = integerRe.ReplaceAllString(query, "BIGINT")
	}
	if strings.Contains(strings.ToUpper(query), "BLOB") {
		query = blobRe.ReplaceAllString(query, "BYTEA")
	}
	if strings.Contains(query, "unixepoch") {
		query = unixepochRe.ReplaceAllString(
			query,
			"extract(epoch from now())::bigint",
		)
	}
	if strings.Contains(query, "strftime") {
		query = strftimeTodayRe.ReplaceAllString(
			query,
			"extract(epoch from date_trunc('day', now()))::bigint",
		)
	}
	if strings.Contains(strings.ToLower(query), "instr") {
		query = instrRe.ReplaceAllString(query, "strpos(")
	}
	if strings.Contains(query, "sqlite_sequence") {
		query = strings.Replace(
			query,
			"UPDATE sqlite_sequence\n    SET seq = (SELECT MAX(id) FROM deployment_logs)\n    WHERE name = 'deployment_logs';",
			"SELECT setval(pg_get_serial_sequence('deployment_logs', 'id'), (SELECT MAX(id) FROM deployment_logs), true);",
			1,
		)
	}
	if strings.ContainsRune(query, '?') {
		query = rewritePlaceholders(query)
	}
	return query
}

// rewritePlaceholders renumbers SQLite placeholders to PostgreSQL
// placeholders, skipping `?` characters that appear inside single- or
// double-quoted literals. It preserves sqlc/SQLite numbered placeholders
// (`?1` -> `$1`) so repeated references keep pointing at the same arg.
func rewritePlaceholders(query string) string {
	var b strings.Builder
	b.Grow(len(query) + 8)
	n := 0
	inSingle, inDouble := false, false
	for i := 0; i < len(query); i++ {
		c := query[i]
		switch {
		case inSingle:
			b.WriteByte(c)
			if c == '\'' {
				inSingle = false
			}
		case inDouble:
			b.WriteByte(c)
			if c == '"' {
				inDouble = false
			}
		case c == '\'':
			inSingle = true
			b.WriteByte(c)
		case c == '"':
			inDouble = true
			b.WriteByte(c)
		case c == '?':
			start := i + 1
			end := start
			for end < len(query) && query[end] >= '0' && query[end] <= '9' {
				end++
			}
			if end > start {
				idx, err := strconv.Atoi(query[start:end])
				if err == nil && idx > 0 {
					if idx > n {
						n = idx
					}
					b.WriteByte('$')
					b.WriteString(strconv.Itoa(idx))
					i = end - 1
					continue
				}
			}
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}
