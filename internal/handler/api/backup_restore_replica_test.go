//go:build e2e && backuprestore && linux

package api_test

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"durpdeploy/internal/testutil"
	"github.com/moby/moby/api/types/container"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Match the deployed 0.3 backup format. Pin the Linux amd64 image content.
const restoreLitestreamImage = "docker.io/litestream/litestream:0.3.13@" +
	"sha256:a932d9801f9d8f11ed0566aa105ef866c097c00ef1191b88a8aa0ff8e78b4071"

type restoreReplica struct {
	container testcontainers.Container
	fixture   *restoreFixture
}

func (f *restoreFixture) replicate(t *testing.T) *restoreReplica {
	t.Helper()
	config := `dbs:
  - path: /recovery/source/app.db
    replicas:
      - type: file
        path: /recovery/backup/replica
        sync-interval: 100ms
`
	if err := os.WriteFile(filepath.Join(f.root, "litestream.yml"),
		[]byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	c := f.litestreamContainer(
		t,
		"replicate",
		"-config",
		"/recovery/litestream.yml",
	)
	return &restoreReplica{c, f}
}

func (f *restoreFixture) litestreamContainer(
	t *testing.T,
	args ...string,
) testcontainers.Container {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	c, err := testcontainers.GenericContainer(ctx,
		testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Image: restoreLitestreamImage,
				Cmd:   args,
				ConfigModifier: func(config *container.Config) {
					config.User = fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
					if os.Getenv("DURPDEPLOY_CONTAINER_RUNTIME") == "podman" {
						// Rootless container root maps to the fixture owner.
						config.User = "0:0"
					}
				},
				HostConfigModifier: func(config *container.HostConfig) {
					config.Binds = []string{f.root + ":/recovery:Z"}
					config.NetworkMode = "none"
					config.ReadonlyRootfs = true
					config.CapDrop = []string{"ALL"}
				},
			},
			Started: true,
		})
	testutil.CleanupContainer(t, c)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (r *restoreReplica) command(t *testing.T, args ...string) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	code, output, err := r.container.Exec(
		ctx,
		append([]string{"litestream"}, args...),
	)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(io.LimitReader(output, 32768))
	if err != nil {
		t.Fatal(err)
	}
	return code, string(data)
}

func (r *restoreReplica) awaitFresh(t *testing.T, projectID int64) {
	t.Helper()
	probe := filepath.Join(r.fixture.root, "backup", "probe.db")
	deadline := time.Now().Add(time.Minute)
	for {
		code, output := r.command(
			t,
			"restore",
			"-config",
			"/recovery/litestream.yml",
			"-o",
			"/recovery/backup/probe.db",
			"/recovery/source/app.db",
		)
		fresh := code == 0 && restoredWatermark(t, probe, projectID)
		if err := os.Remove(probe); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if fresh {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("replica did not recover the final API write: %s", output)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func restoredWatermark(t *testing.T, path string, id int64) bool {
	t.Helper()
	conn, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	}()
	var count int
	if err := conn.QueryRowContext(
		t.Context(),
		"SELECT COUNT(*) FROM projects WHERE id = ? AND name = 'backup-watermark'",
		id,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count == 1
}

func (r *restoreReplica) recover(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	if err := r.container.Stop(ctx, nil); err != nil {
		t.Fatal(err)
	}
	f := r.fixture
	f.assertSQLiteCorrupt(t)
	f.recoverKey(t)
	// The database and its sidecars are gone. Only replica data can recover it.
	restored := f.litestreamContainer(t, "restore", "-config",
		"/recovery/litestream.yml", "-o", "/recovery/restored/app.db",
		"/recovery/source/app.db")
	if code := restoreContainerExit(t, restored); code != 0 {
		t.Fatalf("restore failed with exit %d", code)
	}
	f.database = filepath.Join(f.root, "restored", "app.db")
	f.dsn = restoreSQLiteDSN(f.database)
	conn, err := sql.Open("sqlite", "file:"+f.database+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	}()
	var integrity string
	if err := conn.QueryRowContext(t.Context(), "PRAGMA integrity_check").
		Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("restored database integrity: %q (%v)", integrity, err)
	}
}

func restoreContainerExit(t *testing.T, c testcontainers.Container) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if err := wait.ForExit().WaitUntilReady(ctx, c); err != nil {
		t.Fatal(err)
	}
	state, err := c.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return state.ExitCode
}
