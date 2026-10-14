package dbconfig_test

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"durpdeploy/internal/dbconfig"
	"durpdeploy/internal/migrate"
)

func TestConnectionSecurity(t *testing.T) {
	for _, key := range []string{"PGSSLMODE", "PGSSLROOTCERT", "PGSERVICE", "PGSERVICEFILE", "PGHOST"} {
		t.Setenv(key, "")
	}
	for _, tc := range []struct{ name, dsn, backend, policy string }{
		{"sqlite", ":memory:", "sqlite", "local"},
		{"postgres verified", "postgres://localhost/db?sslmode=verify-full", "postgres", "tls_verified"},
		{"postgres unverified", "postgres://localhost/db?sslmode=require", "postgres", "tls_unverified"},
		{"postgres plaintext", "postgres://localhost/db?sslmode=disable", "postgres", "plaintext_allowed"},
		{"postgres fallback", "postgres://localhost/db?sslmode=prefer", "postgres", "plaintext_allowed"},
		{"postgres default", "postgres://localhost/db", "postgres", "plaintext_allowed"},
		{"postgres socket", "host=/tmp user=app sslmode=disable", "postgres", "local"},
		{"postgres mixed hosts", "host=/tmp,localhost user=app sslmode=disable", "postgres", "plaintext_allowed"},
		{"sqlserver default", "sqlserver://localhost", "sqlserver", "tls_verified"},
		{"sqlserver unverified", "sqlserver://localhost?Encrypt=true&TrustServerCertificate=true", "sqlserver", "tls_unverified"},
		{"sqlserver optional", "sqlserver://localhost?encrypt=false", "sqlserver", "plaintext_allowed"},
		{"sqlserver disabled", "sqlserver://localhost?encrypt=disable", "sqlserver", "plaintext_allowed"},
		{"sqlserver strict", "sqlserver://localhost?encrypt=strict&TrustServerCertificate=true", "sqlserver", "tls_verified"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := dbconfig.InspectConnectionSecurity(tc.dsn)
			if err != nil {
				t.Fatal(err)
			}
			if got.Backend != tc.backend || got.Policy != tc.policy {
				t.Fatalf("unexpected classification: %+v", got)
			}
			warn := tc.policy != "tls_verified" && tc.policy != "local"
			if (got.Warning != "") != warn {
				t.Fatalf(
					"warning presence = %v, want %v",
					got.Warning != "",
					warn,
				)
			}
		})
	}
}

func TestConnectionSecurityPostgresEnvironment(t *testing.T) {
	t.Setenv("PGSSLMODE", "verify-full")
	t.Setenv("PGSSLROOTCERT", "")
	t.Setenv("PGSERVICE", "")
	got, err := dbconfig.InspectConnectionSecurity("postgres://localhost/db")
	if err != nil || got.Policy != "tls_verified" {
		t.Fatalf("environment TLS policy: %+v, %v", got, err)
	}
	got, err = dbconfig.InspectConnectionSecurity(
		"postgres://localhost/db?sslmode=disable",
	)
	if err != nil || got.Policy != "plaintext_allowed" || got.Warning == "" {
		t.Fatalf("explicit override: %+v, %v", got, err)
	}
}

func TestConnectionSecurityErrorsAndWarningsExcludeCredentials(t *testing.T) {
	const secret = "db-password-must-remain-private"
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	for _, dsn := range []string{
		"postgres://app:" + secret + "@localhost/db?sslmode=" + secret,
		"postgres://app:" + secret + "@localhost/db?default_query_exec_mode=" + secret,
		"postgres://app:" + secret + "@localhost/db?statement_cache_capacity=" + secret,
		"postgres://app:" + secret + "@localhost/db?description_cache_capacity=" + secret,
		"sqlserver://app:" + secret + "@localhost?encrypt=" + secret,
	} {
		if _, err := migrate.Run(dsn); !errors.Is(
			err,
			dbconfig.ErrConnectionSecurity,
		) ||
			strings.Contains(err.Error(), secret) {
			t.Fatalf("expected safe configuration error: %v", err)
		}
	}
	// A refused port still exercises the startup warning before connecting.
	_, err := migrate.Run(
		"postgres://app:" + secret + "@127.0.0.1:1/db?sslmode=disable&connect_timeout=1",
	)
	if err == nil {
		t.Fatal("expected the unavailable database to reject connection")
	}
	if !strings.Contains(output.String(), "plaintext_allowed") ||
		strings.Contains(output.String(), secret) {
		t.Fatal("missing warning or exposed credentials")
	}
}
