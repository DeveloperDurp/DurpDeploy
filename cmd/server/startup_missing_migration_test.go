package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"path/filepath"
	"regexp"
	"testing"

	"durpdeploy/internal/migrate"
	"durpdeploy/internal/testutil"
)

func TestStartupMissingMigrationProcessE2E(t *testing.T) {
	dir := t.TempDir()
	dsn := filepath.Join(dir, "upgrade.db")
	env := startupChildEnv(map[string]string{
		"DURPDEPLOY_DB":   dsn,
		"DURPDEPLOY_ADDR": "127.0.0.1:0",
		"DURPDEPLOY_URL":  "http://localhost",
		"DURPDEPLOY_SECRET_KEY": base64.StdEncoding.EncodeToString(
			make([]byte, 32),
		),
		"DURPDEPLOY_CONTAINER_ENABLED":  "false",
		"DURPDEPLOY_AGENT_LISTEN_ADDR":  "127.0.0.1:0",
		"DURPDEPLOY_AGENT_PUBLIC_URL":   "https://localhost",
		"DURPDEPLOY_AGENT_IDENTITY_DIR": filepath.Join(dir, "identity"),
	})
	runStartupCLI(t, dir, env, "admin", "create",
		"--email", "upgrade@test.local", "--password", "upgrade-password-1234")
	output := runStartupCLI(t, dir, env, "tokens", "create",
		"--user", "upgrade@test.local", "--name", "upgrade")
	token := regexp.MustCompile(`ddp_pat_[0-9a-f]{64}`).
		FindString(string(output))
	if token == "" {
		t.Fatal("startup CLI did not create a bearer token")
	}
	conn, err := migrate.Run(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testutil.Close(t, conn) })
	for _, query := range []string{
		"INSERT INTO projects(name) VALUES('retained upgrade project')",
		"DROP TABLE local_administration_lock",
		"DELETE FROM goose_db_version WHERE version_id=67",
	} {
		if _, err := conn.Exec(query); err != nil {
			t.Fatal(err)
		}
	}

	base := startConfiguredProcess(t, dir, env)
	data := startupAPI(t, base, token, http.MethodGet,
		"/api/v1/healthz", "", http.StatusOK)
	var health struct{ OK bool }
	if err := json.Unmarshal(data, &health); err != nil || !health.OK {
		t.Fatalf("health response = %s, error = %v", data, err)
	}
	data = startupAPI(t, base, token, http.MethodGet,
		"/api/v1/projects", "", http.StatusOK)
	var projects struct{ Items []struct{ Name string } }
	if err := json.Unmarshal(data, &projects); err != nil ||
		len(projects.Items) != 1 ||
		projects.Items[0].Name != "retained upgrade project" {
		t.Fatalf("retained projects response = %s, error = %v", data, err)
	}
	var seed int
	if err := conn.QueryRow(
		"SELECT version FROM local_administration_lock WHERE id=1",
	).Scan(&seed); err != nil || seed != 0 {
		t.Fatalf("restored lock seed = %d, error = %v", seed, err)
	}
}
