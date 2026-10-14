//go:build e2e && dbtls && linux

package api_test

import (
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/dbconfig"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/server"
	"github.com/robfig/cron/v3"
)

func (certificates databaseTLSCertificates) connectionDSN(
	t *testing.T,
	dsn, mode string,
) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Scheme == "postgres" {
		switch mode {
		case "verified":
			q.Set("sslmode", "verify-full")
			q.Set("sslrootcert", certificates.ca)
		case "unverified":
			q.Set("sslmode", "require")
		case "plaintext":
			q.Set("sslmode", "disable")
		}
		q.Set("connect_timeout", "5")
	} else {
		switch mode {
		case "verified":
			q.Del("encrypt")
			q.Del("trustservercertificate")
			q.Set("certificate", certificates.ca)
		case "unverified":
			q.Set("encrypt", "true")
			q.Set("trustservercertificate", "true")
		case "plaintext":
			q.Set("encrypt", "false")
		}
		q.Set("connection timeout", "5")
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func (tc databaseTLSCase) assertSession(
	t *testing.T,
	conn *sql.DB,
	backend string,
) {
	t.Helper()
	if backend == "postgres" {
		var encrypted bool
		if err := conn.QueryRowContext(t.Context(), "SELECT ssl FROM pg_stat_ssl WHERE pid = pg_backend_pid()").Scan(&encrypted); err != nil ||
			encrypted != tc.tls {
			t.Fatalf(
				"PostgreSQL session encryption=%v want=%v error=%v",
				encrypted,
				tc.tls,
				err,
			)
		}
		return
	}
	var encrypted string
	if err := conn.QueryRowContext(t.Context(), "SELECT encrypt_option FROM sys.dm_exec_connections WHERE session_id = @@SPID").Scan(&encrypted); err != nil ||
		(encrypted == "TRUE") != tc.tls {
		t.Fatalf(
			"SQL Server session encryption=%s want=%v error=%v",
			encrypted,
			tc.tls,
			err,
		)
	}
}

func (tc databaseTLSCase) assertAPI(t *testing.T, conn *sql.DB, dsn string) {
	t.Helper()
	t.Setenv("DURPDEPLOY_DB", dsn)
	repo := repository.New(conn)
	user := seedAPIUser(t, repo, tc.policy+"@database-tls.test", "admin")
	_, token := seedAPIToken(t, repo, user.ID)
	rnr := runner.New(repo, runner.NewLogBroker())
	srv := httptest.NewServer(server.NewRouter(repo, rnr,
		cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
		handler.NewAuthHandler(repo)))
	t.Cleanup(srv.Close)
	client := &http.Client{Timeout: 10 * time.Second}
	for _, path := range []string{"/api/v1/admin/database-security", "/api/v1/projects"} {
		method := http.MethodGet
		body := ""
		want := http.StatusOK
		if path == "/api/v1/projects" {
			method, body, want = http.MethodPost, `{"name":"database-tls-`+tc.policy+`"}`, http.StatusCreated
		}
		req, err := http.NewRequest(
			method,
			srv.URL+path,
			strings.NewReader(body),
		)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, readErr := io.ReadAll(resp.Body)
		if err := resp.Body.Close(); err != nil {
			t.Fatal(err)
		}
		if readErr != nil || resp.StatusCode != want {
			t.Fatalf(
				"API status=%d want=%d read=%v",
				resp.StatusCode,
				want,
				readErr,
			)
		}
		if path == "/api/v1/admin/database-security" {
			var security dbconfig.ConnectionSecurity
			mustDecode(t, strings.NewReader(string(data)), &security)
			if security.Policy != tc.policy ||
				(security.Warning != "") != (tc.policy != "tls_verified") {
				t.Fatalf("unexpected API security response: %+v", security)
			}
			u, err := url.Parse(dsn)
			if err != nil {
				t.Fatal(err)
			}
			password, _ := u.User.Password()
			if strings.Contains(string(data), password) ||
				strings.Contains(string(data), u.Host) {
				t.Fatal("security API exposes database connection details")
			}
		}
	}
}
