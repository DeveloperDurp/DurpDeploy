//go:build e2e && backuprestore && linux

package api_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/secret"
	"github.com/google/uuid"
)

type restoreFixture struct {
	api                 *artifactE2E
	root, database, key string
	dsn                 string
	backend             restoreBackend
	native              *restoreNativeDB
	binary, secret      string
	password            string
	releaseID           int64
	smtp                *restoreSMTP
}

func newRestoreFixture(t *testing.T) *restoreFixture {
	t.Helper()
	backend := selectedRestoreBackend(t)
	if runtime.GOARCH != "amd64" {
		t.Fatal("recovery drill requires Linux amd64")
	}
	provider := os.Getenv("DOCKER_HOST")
	configured := os.Getenv("DURPDEPLOY_CONTAINER_URL")
	if provider == "" || (configured != "" && configured != provider) {
		t.Fatal(
			"run scripts/test-backup-restore.sh to align the runtime and cleanup provider",
		)
	}
	// Applications, backup containers, and interruption cleanup use one daemon.
	t.Setenv("DURPDEPLOY_CONTAINER_URL", provider)
	// LoadKey prefers this fixed path. Never use an operator's installed key.
	if _, err := os.Stat(secret.KeyFilePath); !os.IsNotExist(err) {
		t.Fatal("recovery drill requires an absent /etc/durpdeploy/key")
	}
	kind := os.Getenv("DURPDEPLOY_CONTAINER_RUNTIME")
	if kind != "docker" && kind != "podman" {
		t.Fatal("set DURPDEPLOY_CONTAINER_RUNTIME to docker or podman")
	}
	session := os.Getenv("TESTCONTAINERS_SESSION_ID")
	if !strings.HasPrefix(session, "durpdeploy-go-test.") {
		t.Fatal("run the recovery drill with scripts/go_test.sh")
	}
	if filepath.Base(os.Getenv("DURPDEPLOY_GO_TEST_DIRECTORY")) != session {
		t.Fatal("recovery drill requires the runner's owned session directory")
	}
	// The shell runner sweeps this reserved namespace even after interruption.
	t.Setenv("DURPDEPLOY_CONTAINER_NAMESPACE",
		strings.ReplaceAll(session, ".", "-")+"-restore")
	root, err := os.MkdirTemp(
		os.Getenv("DURPDEPLOY_GO_TEST_DIRECTORY"),
		"restore-",
	)
	if err != nil {
		t.Fatal(err)
	}
	f := &restoreFixture{root: root, backend: backend}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	for _, name := range []string{"source", "backup", "restored", "tmp"} {
		if err := os.Mkdir(filepath.Join(f.root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	f.database = filepath.Join(f.root, "source", "app.db")
	f.dsn = restoreSQLiteDSN(f.database)
	switch backend {
	case restoreSQLiteBackend:
	case restorePostgresBackend:
		f.native = restorePostgres(t)
		f.dsn = f.native.appDSN
	case restoreMSSQLBackend:
		f.native = restoreMSSQL(t, true)
		f.dsn = f.native.appDSN
	default:
		t.Fatal("unsupported recovery backend")
	}
	f.key = filepath.Join(f.root, "source", "key")
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.key,
		[]byte(base64.StdEncoding.EncodeToString(key)), 0600); err != nil {
		t.Fatal(err)
	}
	f.secret = uuid.NewString()
	conn, err := migrate.Run(f.dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	}()
	repo := repository.New(conn)
	// Bootstrap only authentication. All application fixtures use public APIs.
	token := f.seedAuth(t, repo)
	f.api = &artifactE2E{token: token, client: &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
	f.smtp = restoreSMTPFixture(t)
	f.binary = filepath.Join(f.root, "durpdeploy")
	buildCtx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(buildCtx, "go", "build", "-race",
		"-o", f.binary, "../../../cmd/server")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build recovery server: %v\n%s", err, output)
	}
	t.Cleanup(func() {
		filter := "--filter=label=io.durpdeploy.namespace=" + kind + ":" +
			os.Getenv("DURPDEPLOY_CONTAINER_NAMESPACE")
		ids := stagingRuntime(t, "ps", "--all", "--quiet", filter)
		if ids != "" {
			stagingRuntime(t, append([]string{"rm", "--force", "--volumes"},
				strings.Fields(ids)...)...)
		}
		volumes := stagingRuntime(t, "volume", "ls", "--quiet", filter)
		if volumes != "" {
			stagingRuntime(t, append([]string{"volume", "rm", "--force"},
				strings.Fields(volumes)...)...)
		}
	})
	return f
}

func (f *restoreFixture) environment(t *testing.T) []string {
	t.Helper()
	key, err := os.ReadFile(f.key)
	if err != nil {
		t.Fatal(err)
	}
	// Inherit runtime access, but exclude operator application configuration.
	var env []string
	for _, name := range []string{
		"PATH", "HOME", "XDG_RUNTIME_DIR", "DOCKER_HOST", "CONTAINER_HOST",
		"DURPDEPLOY_CONTAINER_RUNTIME", "DURPDEPLOY_CONTAINER_URL",
		"DURPDEPLOY_CONTAINER_NAMESPACE",
	} {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	host, port, err := net.SplitHostPort(f.smtp.address)
	if err != nil {
		t.Fatal(err)
	}
	return append(env,
		"DURPDEPLOY_DB="+f.dsn,
		"DURPDEPLOY_SECRET_KEY="+string(key),
		"DURPDEPLOY_ADDR=127.0.0.1:0",
		"DURPDEPLOY_URL=http://localhost",
		"DURPDEPLOY_AGENT_LISTEN_ADDR=127.0.0.1:0",
		"DURPDEPLOY_AGENT_PUBLIC_URL=https://localhost",
		"DURPDEPLOY_AGENT_IDENTITY_DIR="+filepath.Join(f.root, "identity"),
		"DURPDEPLOY_EXECUTION_BOUNDARY=development",
		"DURPDEPLOY_SMTP_HOST="+host,
		"DURPDEPLOY_SMTP_PORT="+port,
		"DURPDEPLOY_SMTP_FROM=sender@example.test",
		"TMPDIR="+filepath.Join(f.root, "tmp"),
	)
}

func restoreSQLiteDSN(path string) string {
	return path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)"
}
