package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/migrate"
)

const startupChildMarker = "DURPDEPLOY_STARTUP_TEST_CHILD"

func TestStartupConfigurationChild(t *testing.T) {
	if os.Getenv(startupChildMarker) != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{os.Args[0]}, os.Args[i+1:]...)
			main()
			return
		}
	}
	t.Fatal("child command separator is missing")
}

func startupChildEnv(overrides map[string]string) []string {
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "DURPDEPLOY_") &&
			!strings.HasPrefix(key, "DURPDEPLOY_GO_TEST_") {
			continue
		}
		env = append(env, entry)
	}
	env = append(env, startupChildMarker+"=1")
	for key, value := range overrides {
		env = append(env, key+"="+value)
	}
	return env
}

func startupChildCommand(
	ctx context.Context,
	dir string,
	env []string,
	args ...string,
) *exec.Cmd {
	flags := append(
		[]string{"-test.run=^TestStartupConfigurationChild$", "--"},
		args...)
	cmd := exec.CommandContext(ctx, os.Args[0], flags...)
	cmd.Dir = dir
	cmd.Env = append(env, "TMPDIR="+dir)
	return cmd
}

func runStartupCLI(
	t *testing.T,
	dir string,
	env []string,
	args ...string,
) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	output, err := startupChildCommand(ctx, dir, env, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("startup CLI command failed: %v", err)
	}
	return output
}

func startConfiguredProcess(t *testing.T, dir string, env []string) string {
	t.Helper()
	logPath := filepath.Join(dir, "server.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := startupChildCommand(ctx, dir, env)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		cancel()
		_ = logFile.Close()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait(); close(done) }()
	t.Cleanup(func() {
		defer cancel()
		defer func() { _ = logFile.Close() }()
		stopConfiguredProcess(t, cmd, done)
	})
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			t.Fatalf("configured server exited before readiness: %v", err)
		default:
		}
		if addr := startupReadyAddress(logPath); addr != "" {
			return "http://" + addr
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("configured server readiness timed out")
	return ""
}

func startupReadyAddress(path string) string {
	data, _ := os.ReadFile(path)
	for _, line := range bytes.Split(data, []byte("\n")) {
		var record struct{ Msg, Addr string }
		if json.Unmarshal(line, &record) == nil &&
			record.Msg == "server starting" {
			return record.Addr
		}
	}
	return ""
}

func stopConfiguredProcess(t *testing.T, cmd *exec.Cmd, done <-chan error) {
	t.Helper()
	select {
	case <-done:
		return
	default:
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("configured server shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		t.Error("configured server did not stop after SIGTERM")
	}
}

func startupAPI(
	t *testing.T,
	base, token, method, path, body string,
	status int,
) []byte {
	t.Helper()
	request, err := http.NewRequestWithContext(
		t.Context(),
		method,
		base+path,
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Forwarded-For", "203.0.113.45")
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != status {
		t.Fatalf(
			"%s %s: status=%d, expected=%d, read error=%v",
			method,
			path,
			response.StatusCode,
			status,
			err,
		)
	}
	return data
}

func TestStartupConfigurationProcessE2E(t *testing.T) {
	for _, override := range []bool{false, true} {
		t.Run(strconv.FormatBool(override), func(t *testing.T) {
			exerciseStartupProcessCase(t, override)
		})
	}
}

func exerciseStartupProcessCase(t *testing.T, override bool) {
	t.Helper()
	dir := t.TempDir()
	yamlDB := filepath.Join(dir, "yaml.db")
	file := filepath.Join(dir, "startup.yaml")
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	source := fmt.Sprintf(
		"db: %q\naddr: '127.0.0.1:0'\nurl: 'http://localhost'\nsecret_key: %q\n"+
			"trusted_proxies: '192.0.2.10/32'\nextra_scrub_patterns: 'startup-secret-pattern'\n"+
			"container:\n  enabled: false\nagent_listener:\n  listen_addr: '127.0.0.1:0'\n"+
			"  public_url: 'https://localhost'\n  identity_dir: %q\n"+
			"limits:\n  token_telemetry:\n    concurrency: 2\n"+
			"    interval_seconds: 120\n    write_timeout_seconds: 1\n"+
			"  http:\n    read_header_timeout_seconds: 1\n    read_timeout_seconds: 2\n"+
			"    write_timeout_seconds: 2\n    idle_timeout_seconds: 3\n    stream_write_timeout_seconds: 1\n"+
			"    json_bytes: 1024\n    form_bytes: 4096\n    password_form_bytes: 512\n    repository_form_bytes: 768\n",
		yamlDB,
		key,
		filepath.Join(dir, "identity"),
	)
	if err := os.WriteFile(file, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{"DURPDEPLOY_CONFIG_FILE": file}
	if override {
		values["DURPDEPLOY_DB"] = filepath.Join(dir, "environment.db")
		values["DURPDEPLOY_ADDR"] = "127.0.0.1:0"
		values["DURPDEPLOY_TRUSTED_PROXIES"] = "127.0.0.1/32"
		values["DURPDEPLOY_LIMITS_TOKEN_TELEMETRY_INTERVAL_SECONDS"] = "10"
		values["DURPDEPLOY_LIMITS_HTTP_READ_HEADER_TIMEOUT_SECONDS"] = "3"
		values["DURPDEPLOY_LIMITS_HTTP_READ_TIMEOUT_SECONDS"] = "4"
		values["DURPDEPLOY_LIMITS_HTTP_JSON_BYTES"] = "2048"
	}
	env := startupChildEnv(values)
	email := "startup-" + strconv.FormatBool(override) + "@test.local"
	runStartupCLI(
		t,
		dir,
		env,
		"admin",
		"create",
		"--email",
		email,
		"--password",
		"startup-password-1234",
	)
	output := runStartupCLI(
		t,
		dir,
		env,
		"tokens",
		"create",
		"--user",
		email,
		"--name",
		"startup-e2e",
	)
	token := regexp.MustCompile(`ddp_pat_[0-9a-f]{64}`).
		FindString(string(output))
	if token == "" {
		t.Fatal("configured CLI did not create a bearer token")
	}
	dsn := yamlDB
	if override {
		dsn = values["DURPDEPLOY_DB"]
	}
	observed := seedStartupTokenUsage(t, dsn, token)
	base := startConfiguredProcess(t, dir, env)
	assertStartupHeaderDeadline(t, base, override)
	assertStartupBodyLimit(t, base, token, override)
	assertStartupTokenPolicy(t, base, token, observed, override)
	assertStartupAPI(t, base, token, email, override)
	if _, err := os.Stat(filepath.Join(dir, "durpdeploy.db")); !os.IsNotExist(
		err,
	) {
		t.Fatal("configured commands wrote to the default database")
	}
	if override {
		if _, err := os.Stat(yamlDB); !os.IsNotExist(err) {
			t.Fatal("environment override still created the YAML database")
		}
	}
}

func assertStartupBodyLimit(t *testing.T, base, token string, override bool) {
	t.Helper()
	limit := 1024
	if override {
		limit = 2048
	}
	for _, size := range []int{limit, limit + 1} {
		body := `{"name":"configured-body"}`
		req, err := http.NewRequestWithContext(
			t.Context(),
			"POST",
			base+"/api/v1/projects",
			strings.NewReader(body+strings.Repeat(" ", size-len(body))),
		)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, readErr := io.Copy(io.Discard, response.Body)
		closeErr := response.Body.Close()
		want := http.StatusCreated
		if size > limit {
			want = http.StatusRequestEntityTooLarge
		}
		if readErr != nil || closeErr != nil || response.StatusCode != want {
			t.Fatalf(
				"configured JSON size=%d status=%d read=%v close=%v",
				size,
				response.StatusCode,
				readErr,
				closeErr,
			)
		}
	}
}

func assertStartupHeaderDeadline(t *testing.T, base string, override bool) {
	t.Helper()
	conn, err := net.DialTimeout(
		"tcp",
		strings.TrimPrefix(base, "http://"),
		time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, err := io.WriteString(conn, "GET /healthz HTTP/1.1\r\nHost:"); err != nil {
		t.Fatal(err)
	}
	if !override {
		if _, err := io.ReadAll(conn); err != nil {
			t.Fatalf(
				"YAML header deadline did not close the connection: %v",
				err,
			)
		}
		if time.Since(started) > 3*time.Second {
			t.Fatal("the configured one-second header deadline was not applied")
		}
		return
	}
	time.Sleep(1500 * time.Millisecond)
	if _, err := io.WriteString(conn, " localhost\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("environment header deadline did not override YAML: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("completed headers returned status %d", response.StatusCode)
	}
}

func seedStartupTokenUsage(t *testing.T, dsn, token string) int64 {
	t.Helper()
	conn, err := migrate.Run(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	observed := time.Now().Unix() - 90
	if _, err := conn.ExecContext(t.Context(),
		"UPDATE api_tokens SET last_used_at = ? WHERE token_hash = ?",
		observed, auth.HashApiToken(token),
	); err != nil {
		t.Fatal(err)
	}
	return observed
}

func assertStartupTokenPolicy(
	t *testing.T, base, token string, observed int64, refresh bool,
) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		data := startupAPI(t, base, token, "GET", "/api/v1/tokens", "", 200)
		var response struct {
			Items []struct {
				LastUsedAt int64 `json:"last_used_at"`
			}
		}
		if err := json.Unmarshal(data, &response); err != nil ||
			len(response.Items) != 1 {
			t.Fatal("token API did not return the configured token")
		}
		stamp := response.Items[0].LastUsedAt
		if !refresh && stamp != observed {
			t.Fatal("token telemetry ignored the longer YAML interval")
		}
		if refresh && stamp > observed {
			return
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if refresh {
		t.Fatal("token telemetry ignored the shorter environment interval")
	}
}

func assertStartupAPI(t *testing.T, base, token, email string, trusted bool) {
	t.Helper()
	who := startupAPI(t, base, token, "GET", "/api/v1/users/me", "", 200)
	if !bytes.Contains(who, []byte(email)) {
		t.Fatal("CLI and server did not use the same configured database")
	}
	security := startupAPI(
		t,
		base,
		token,
		"GET",
		"/api/v1/admin/database-security",
		"",
		200,
	)
	if !bytes.Contains(security, []byte(`"sqlite"`)) {
		t.Fatal("database security API did not use configured DSN")
	}
	project := startupAPI(
		t,
		base,
		token,
		"POST",
		"/api/v1/projects",
		`{"name":"startup-policy"}`,
		201,
	)
	var row struct{ ID int64 }
	if err := json.Unmarshal(project, &row); err != nil || row.ID == 0 {
		t.Fatal("project creation did not return an ID")
	}
	path := fmt.Sprintf("/api/v1/projects/%d/releases", row.ID)
	response := startupAPI(t, base, token, "POST", path,
		`{"version":"v1","artifact_ref":"startup-secret-pattern"}`, 422)
	if bytes.Contains(response, []byte("startup-secret-pattern")) {
		t.Fatal("configured provenance rejection exposed its input")
	}
	audit := startupAPI(t, base, token, "GET", "/api/v1/admin/audit", "", 200)
	if bytes.Contains(audit, []byte("203.0.113.45")) != trusted {
		t.Fatal("audit API did not use the configured trusted-proxy policy")
	}
}

func TestStartupConfigurationBadFileAndCommandIndependence(t *testing.T) {
	dir := t.TempDir()
	values := map[string]string{
		"DURPDEPLOY_CONFIG_FILE":        filepath.Join(dir, "absent.yaml"),
		"DURPDEPLOY_DB":                 filepath.Join(dir, "never.db"),
		"DURPDEPLOY_AGENT_IDENTITY_DIR": filepath.Join(dir, "identity"),
	}
	env := startupChildEnv(values)
	for _, args := range [][]string{{"help"}, {"version"}} {
		runStartupCLI(t, dir, env, args...)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	output, err := startupChildCommand(ctx, dir, env).CombinedOutput()
	if err == nil || !bytes.Contains(output, []byte("DURPDEPLOY_CONFIG_FILE")) {
		t.Fatal(
			"invalid startup file did not fail with an actionable diagnostic",
		)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatal(
			"invalid startup configuration created database or identity files",
		)
	}
}
