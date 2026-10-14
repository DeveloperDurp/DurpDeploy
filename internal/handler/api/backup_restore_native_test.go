//go:build e2e && backuprestore && linux

package api_test

import (
	"context"
	"database/sql"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
)

type restoreNativeDB struct {
	container                  testcontainers.Container
	adminDSN, appDSN, password string
}

func restoreLoopback(config *container.HostConfig) {
	for port := range config.PortBindings {
		config.PortBindings[port] = []network.PortBinding{{
			HostIP: netip.MustParseAddr("127.0.0.1"),
		}}
	}
}

func restoreAppDSN(t *testing.T, admin string, password string) string {
	t.Helper()
	u, err := url.Parse(admin)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword("restore_app", password)
	if u.Scheme == "sqlserver" {
		q := u.Query()
		q.Set("database", "restore_app")
		u.RawQuery = q.Encode()
	} else {
		u.Path = "/restore_app"
	}
	return u.String()
}

func restoreEndpoint(
	t *testing.T,
	c testcontainers.Container,
	port string,
) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	host, err := c.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mapped, err := c.MappedPort(ctx, port)
	if err != nil {
		t.Fatal(err)
	}
	return net.JoinHostPort(host, mapped.Port())
}

func restoreOpen(t *testing.T, driver, dsn string) *sql.DB {
	t.Helper()
	conn, err := sql.Open(driver, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	})
	return conn
}

func restoreSQL(t *testing.T, conn *sql.DB, statement string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	if _, err := conn.ExecContext(ctx, statement); err != nil {
		t.Fatalf("native recovery SQL failed: %v", err)
	}
}

func restoreExec(t *testing.T, c testcontainers.Container, args []string,
	options ...tcexec.ProcessOption) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	code, reader, err := c.Exec(ctx, args, options...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(
		io.Discard,
		io.LimitReader(reader, 32768),
	); err != nil {
		t.Fatal(err)
	}
	return code
}

func restoreArchive(t *testing.T, c testcontainers.Container, paths [2]string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	reader, err := c.CopyFileFromContainer(ctx, paths[0])
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reader.Close(); err != nil {
			t.Error(err)
		}
	}()
	file, err := os.OpenFile(paths[1], os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	}()
	if size, err := io.Copy(file, reader); err != nil || size == 0 {
		t.Fatalf("copy native archive: bytes=%d error=%v", size, err)
	}
}

func restoreUpload(t *testing.T, c testcontainers.Container, paths [2]string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	if err := c.CopyFileToContainer(ctx, paths[0], paths[1], 0600); err != nil {
		t.Fatal(err)
	}
}

func restoreTerminate(t *testing.T, c testcontainers.Container) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	if err := c.Terminate(ctx); err != nil {
		t.Fatal(err)
	}
}

func (f *restoreFixture) recoverKey(t *testing.T) {
	t.Helper()
	key, err := os.ReadFile(filepath.Join(f.root, "backup", "key"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(f.root, "source")); err != nil {
		t.Fatal(err)
	}
	f.key = filepath.Join(f.root, "restored", "key")
	if err := os.WriteFile(f.key, key, 0600); err != nil {
		t.Fatal(err)
	}
}
