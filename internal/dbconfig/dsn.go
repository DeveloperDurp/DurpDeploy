// Package dbconfig inspects database connection settings without disclosing them.
package dbconfig

import (
	"net/url"
	"strings"
)

func IsSQLServer(dsn string) bool {
	return strings.HasPrefix(
		strings.ToLower(strings.TrimSpace(dsn)),
		"sqlserver://",
	)
}

func IsPostgres(dsn string) bool {
	return strings.HasPrefix(dsn, "postgres://") ||
		strings.HasPrefix(dsn, "postgresql://") ||
		strings.Contains(dsn, "host=") || strings.Contains(dsn, "user=")
}

// SQLServerDSN preserves explicit overrides and supplies verified TLS defaults.
func SQLServerDSN(dsn string) string {
	parsedDSN := strings.TrimSpace(dsn)
	u, err := url.Parse(parsedDSN)
	if err != nil {
		return parsedDSN
	}
	q := u.Query()
	if !hasQueryOption(q, "encrypt") {
		q.Set("encrypt", "true")
	}
	if !hasQueryOption(q, "trustservercertificate") {
		q.Set("trustservercertificate", "false")
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func hasQueryOption(values url.Values, key string) bool {
	for existing := range values {
		if strings.EqualFold(existing, key) {
			return true
		}
	}
	return false
}
