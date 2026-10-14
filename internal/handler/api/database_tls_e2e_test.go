//go:build e2e && dbtls && linux

package api_test

import (
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"durpdeploy/internal/migrate"
)

type databaseTLSCase struct {
	name, policy string
	tls          bool
}

func TestDatabaseTLSAPIEndToEnd(t *testing.T) {
	for _, key := range []string{"PGSSLMODE", "PGSSLROOTCERT", "PGSERVICE", "PGSERVICEFILE"} {
		t.Setenv(key, "")
	}
	certificates := databaseTLSCerts(t)
	dsn := databaseTLSContainer(t, certificates)
	for _, tc := range []databaseTLSCase{
		{"verified", "tls_verified", true},
		{"unverified", "tls_unverified", true},
		{"plaintext", "plaintext_allowed", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.run(t, dsn, certificates)
		})
	}
	for _, failure := range []string{"untrusted", "hostname"} {
		t.Run(failure, func(t *testing.T) {
			selected := certificates
			if failure == "untrusted" {
				selected.ca = certificates.otherCA
			}
			configured := selected.connectionDSN(t, dsn, "verified")
			if failure == "hostname" {
				u, err := url.Parse(configured)
				if err != nil {
					t.Fatal(err)
				}
				u.Host = net.JoinHostPort("127.0.0.1", u.Port())
				configured = u.String()
			}
			conn, err := migrate.Run(configured)
			if conn != nil {
				if closeErr := conn.Close(); closeErr != nil {
					t.Fatal(closeErr)
				}
			}
			if err == nil {
				t.Fatal("verified mode accepted an invalid certificate")
			}
			// Prove a certificate failure, rather than an unrelated startup error.
			if !strings.Contains(err.Error(), "certificate") {
				t.Fatalf("expected certificate validation failure: %v", err)
			}
		})
	}
}

func (tc databaseTLSCase) run(
	t *testing.T,
	dsn string,
	certificates databaseTLSCertificates,
) {
	t.Helper()
	backend := os.Getenv("DURPDEPLOY_TLS_BACKEND")
	configured := certificates.connectionDSN(t, dsn, tc.name)
	logFile, err := os.OpenFile(
		filepath.Join(t.TempDir(), "startup.log"),
		os.O_CREATE|os.O_RDWR,
		0600,
	)
	if err != nil {
		t.Fatal(err)
	}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(logFile, nil)))
	t.Cleanup(func() {
		slog.SetDefault(previous)
		if err := logFile.Close(); err != nil {
			t.Error(err)
		}
	})
	conn, err := migrate.Run(configured)
	if err != nil {
		t.Fatalf("database TLS startup failed: %v", err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	})
	logs, err := os.ReadFile(logFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(
		string(logs),
		"database transport security warning",
	) != (tc.name != "verified") {
		t.Fatal("startup warning does not match configured security")
	}
	parsed, err := url.Parse(configured)
	if err != nil {
		t.Fatal(err)
	}
	password, _ := parsed.User.Password()
	if strings.Contains(string(logs), password) {
		t.Fatal("startup logs expose database credentials")
	}
	tc.assertSession(t, conn, backend)
	tc.assertAPI(t, conn, configured)
}
