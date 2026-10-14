package dbconfig

import (
	"crypto/tls"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/microsoft/go-mssqldb/msdsn"
)

// ConnectionSecurity describes configured policy, not a live TLS session.
// It deliberately contains no connection strings, addresses, or credentials.
type ConnectionSecurity struct {
	Backend string `json:"backend"`
	Policy  string `json:"policy"`
	Warning string `json:"warning,omitempty"`
}

var ErrConnectionSecurity = errors.New("invalid database TLS configuration")

// InspectConnectionSecurity uses the same parsers and defaults as the drivers.
// PostgreSQL environment variables, service files, and fallbacks are included.
func InspectConnectionSecurity(dsn string) (ConnectionSecurity, error) {
	security := ConnectionSecurity{Backend: "sqlite", Policy: "local"}
	switch {
	case IsSQLServer(dsn):
		security.Backend = "sqlserver"
		config, err := msdsn.Parse(SQLServerDSN(dsn))
		if err != nil {
			return ConnectionSecurity{}, ErrConnectionSecurity
		}
		switch config.Encryption {
		case msdsn.EncryptionRequired, msdsn.EncryptionStrict:
			security.Policy = "tls_verified"
			if config.Parameters[msdsn.ServerCertificate] != "" {
				security.Policy = "tls_pinned"
			} else if config.TrustServerCertificate {
				security.Policy = "tls_unverified"
			}
		case msdsn.EncryptionOff, msdsn.EncryptionDisabled:
			security.Policy = "plaintext_allowed"
		default:
			return ConnectionSecurity{}, ErrConnectionSecurity
		}
	case IsPostgres(dsn):
		security.Backend = "postgres"
		config, err := pgx.ParseConfig(dsn)
		if err != nil {
			// Driver parse errors can include the original DSN or its values.
			return ConnectionSecurity{}, ErrConnectionSecurity
		}
		security.Policy = postgresPolicy(config.Host, config.TLSConfig)
		for _, fallback := range config.Fallbacks {
			policy := postgresPolicy(fallback.Host, fallback.TLSConfig)
			if policyRank(policy) > policyRank(security.Policy) {
				security.Policy = policy
			}
		}
	}
	switch security.Policy {
	case "plaintext_allowed":
		security.Warning = "Database connections may send application data without TLS."
	case "tls_unverified":
		security.Warning = "Database TLS does not verify the server certificate or hostname."
	case "tls_ca_verified":
		security.Warning = "Database TLS verifies the certificate authority but does not verify the hostname."
	case "tls_pinned":
		security.Warning = "Database TLS pins the server certificate without checking its hostname, expiration, or certificate authority."
	}
	return security, nil
}

func postgresPolicy(host string, config *tls.Config) string {
	if strings.HasPrefix(host, "/") {
		return "local"
	}
	return tlsPolicy(config)
}

func tlsPolicy(config *tls.Config) string {
	if config == nil {
		return "plaintext_allowed"
	}
	if config.InsecureSkipVerify {
		if config.VerifyPeerCertificate != nil ||
			config.VerifyConnection != nil {
			return "tls_ca_verified"
		}
		return "tls_unverified"
	}
	return "tls_verified"
}

func policyRank(policy string) int {
	switch policy {
	case "plaintext_allowed":
		return 4
	case "tls_unverified":
		return 3
	case "tls_ca_verified":
		return 2
	case "tls_verified":
		return 1
	default:
		return 0
	}
}
