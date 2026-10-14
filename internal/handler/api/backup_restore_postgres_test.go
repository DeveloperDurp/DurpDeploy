//go:build e2e && backuprestore && linux

package api_test

import (
	"context"
	"crypto/rand"
	"fmt"
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

const restorePostgresImage = "docker.io/library/postgres:16-alpine@" +
	"sha256:1a66d744c1b459e13b05a8fca341da84cb63383e99ce262210efee5a319d4551"

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
