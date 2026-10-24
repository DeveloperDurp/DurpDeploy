//go:build e2e && backuprestore && linux

package api_test

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net/netip"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"durpdeploy/internal/testutil"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/wait"
)

const restorePostgresImage = "harbor.durp.info/dockerhub/library/postgres:16-alpine@" +
	"sha256:1a66d744c1b459e13b05a8fca341da84cb63383e99ce262210efee5a319d4551"

func (db *restoreNativeDB) logPostgresActivity(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Use a reserved administrator connection before stopping the failed app.
	// Report counts and waits only. SQL text can contain credentials or secrets.
	const query = `SET statement_timeout = '3s';
SELECT json_build_object('max_connections', current_setting('max_connections'),
    'reserved_connections', current_setting('superuser_reserved_connections'),
    'sessions', (SELECT json_agg(activity) FROM (
        SELECT CASE usename WHEN 'restore_app' THEN 'application'
                WHEN 'postgres' THEN 'administrator' ELSE 'other' END AS owner,
            backend_type, state, wait_event_type, wait_event,
            CASE WHEN query LIKE '-- name: TouchApiTokenLastUsed%'
                THEN 'token_usage' ELSE 'other' END AS operation,
            count(*) AS connections,
            count(*) FILTER (WHERE cardinality(pg_blocking_pids(pid)) > 0)
                AS blocked,
            max(extract(epoch FROM clock_timestamp() - query_start))
                AS oldest_query_seconds
        FROM pg_stat_activity WHERE pid <> pg_backend_pid()
        GROUP BY 1, 2, 3, 4, 5, 6 ORDER BY connections DESC LIMIT 20
    ) AS activity));`
	code, output, err := db.container.Exec(ctx, []string{
		"psql", "--no-psqlrc", "--tuples-only", "--no-align",
		"-U", "postgres", "-d", "postgres", "-v", "ON_ERROR_STOP=1",
		"-c", query,
	}, tcexec.Multiplexed())
	if err != nil {
		t.Logf("PostgreSQL activity diagnostics unavailable: %v", err)
		return
	}
	data, err := io.ReadAll(io.LimitReader(output, 16<<10))
	t.Logf("PostgreSQL activity: exit=%d read_error=%v\n%s", code, err, data)
}

func restorePostgres(t *testing.T) *restoreNativeDB {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	password := rand.Text()
	c, err := testcontainers.GenericContainer(ctx,
		testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Image:        restorePostgresImage,
				ExposedPorts: []string{"5432/tcp"},
				Env: map[string]string{
					"POSTGRES_PASSWORD": password,
				},
				HostConfigModifier: func(config *container.HostConfig) {
					config.PortBindings = network.PortMap{
						network.MustParsePort("5432/tcp"): {
							{HostIP: netip.MustParseAddr("127.0.0.1")},
						},
					}
				},
				WaitingFor: wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).
					WithStartupTimeout(2 * time.Minute),
			}, Started: true,
		})
	testutil.CleanupContainer(t, c)
	if err != nil {
		t.Fatalf("start recovery PostgreSQL: %v", err)
	}
	restorePrivatePort(t, c)
	dsn := (&url.URL{Scheme: "postgres", User: url.UserPassword("postgres", password),
		Host: restoreEndpoint(t, c, "5432/tcp"), Path: "/postgres",
		RawQuery: "sslmode=disable"}).String()
	db := &restoreNativeDB{container: c, adminDSN: dsn, password: rand.Text()}
	admin := restoreOpen(t, "pgx-qmark", dsn)
	// Passwords contain only the generated base32 alphabet, never SQL input.
	restoreSQL(t, admin, fmt.Sprintf(
		"CREATE ROLE restore_app LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD '%s'",
		db.password,
	))
	restoreSQL(t, admin, "CREATE DATABASE restore_app OWNER restore_app")
	db.appDSN = restoreAppDSN(t, dsn, db.password)
	if err := admin.Close(); err != nil {
		t.Fatal(err)
	}
	return db
}

type restorePostgresBackup struct {
	fixture   *restoreFixture
	archive   string
	watermark int64
}

func (b *restorePostgresBackup) awaitFresh(t *testing.T, id int64) {
	t.Helper()
	b.watermark = id
	db := b.fixture.native
	if code := db.command(
		t,
		[]string{
			"pg_dump",
			"-h",
			"127.0.0.1",
			"-U",
			"restore_app",
			"-d",
			"restore_app",
			"-Fc",
			"-f",
			"/tmp/recovery.dump",
		},
		tcexec.WithEnv([]string{"PGPASSWORD=" + db.password}),
	); code != 0 {
		t.Fatalf("pg_dump failed with exit %d", code)
	}
	b.archive = filepath.Join(b.fixture.root, "backup", "postgres.dump")
	restoreArchive(t, db.container, [2]string{"/tmp/recovery.dump", b.archive})
}

func (b *restorePostgresBackup) recover(t *testing.T) {
	t.Helper()
	restoreTerminate(t, b.fixture.native.container)
	b.fixture.recoverKey(t)
	for _, path := range []string{"", restoreTruncatedArchive(t, b.archive)} {
		failed := restorePostgres(t)
		archive := "/tmp/absent.dump"
		if path != "" {
			archive = "/tmp/recovery.dump"
			restoreUpload(t, failed.container, [2]string{path, archive})
		}
		if restorePostgresArchive(t, failed, archive) == 0 {
			t.Fatal("pg_restore accepted a missing or truncated archive")
		}
		restoreTerminate(t, failed.container)
	}
	target := restorePostgres(t)
	restoreUpload(
		t,
		target.container,
		[2]string{b.archive, "/tmp/recovery.dump"},
	)
	if code := restorePostgresArchive(
		t,
		target,
		"/tmp/recovery.dump",
	); code != 0 {
		t.Fatalf("pg_restore failed with exit %d", code)
	}
	b.fixture.native = target
	b.fixture.dsn = target.appDSN
	b.fixture.assertNativeRestored(t, b.watermark)
}

func restorePostgresArchive(
	t *testing.T,
	db *restoreNativeDB,
	path string,
) int {
	t.Helper()
	return db.command(
		t,
		[]string{"pg_restore", "-h", "127.0.0.1",
			"-U", "restore_app", "-d", "restore_app", "--single-transaction",
			"--exit-on-error", "--no-owner", "--no-acl", path},
		tcexec.WithEnv([]string{"PGPASSWORD=" + db.password}),
	)
}
