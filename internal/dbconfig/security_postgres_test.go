package dbconfig_test

import (
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"durpdeploy/internal/dbconfig"
)

func TestConnectionSecurityCertificatePolicies(t *testing.T) {
	t.Setenv("PGSERVICE", "")
	t.Setenv("PGSSLROOTCERT", "")
	srv := httptest.NewTLSServer(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
	)
	t.Cleanup(srv.Close)
	certificate := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(certificate, pem.EncodeToMemory(&pem.Block{
		Type: "CERTIFICATE", Bytes: srv.Certificate().Raw,
	}), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ dsn, policy string }{
		{"postgres://localhost/db?sslmode=require&sslrootcert=" + url.QueryEscape(certificate), "tls_ca_verified"},
		{"postgres://localhost/db?sslmode=verify-ca&sslrootcert=" + url.QueryEscape(certificate), "tls_ca_verified"},
		{"postgres://localhost/db?sslmode=verify-full&sslrootcert=" + url.QueryEscape(certificate), "tls_verified"},
		{"sqlserver://localhost?encrypt=true&servercertificate=" + url.QueryEscape(certificate), "tls_pinned"},
	} {
		got, err := dbconfig.InspectConnectionSecurity(tc.dsn)
		if err != nil || got.Policy != tc.policy ||
			(got.Warning != "") != (tc.policy != "tls_verified") {
			t.Fatalf(
				"certificate policy=%+v want=%s error=%v",
				got,
				tc.policy,
				err,
			)
		}
	}
}

func TestConnectionSecurityPostgresService(t *testing.T) {
	t.Setenv("PGSSLMODE", "disable")
	t.Setenv("PGSSLROOTCERT", "")
	path := filepath.Join(t.TempDir(), "pg_service.conf")
	if err := os.WriteFile(path, []byte("[tls-test]\nhost=localhost\nuser=app\nsslmode=verify-full\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PGSERVICEFILE", path)
	t.Setenv("PGSERVICE", "tls-test")
	got, err := dbconfig.InspectConnectionSecurity("postgres:///?dbname=app")
	if err != nil || got.Policy != "tls_verified" {
		t.Fatalf("service policy: %+v, %v", got, err)
	}
}
