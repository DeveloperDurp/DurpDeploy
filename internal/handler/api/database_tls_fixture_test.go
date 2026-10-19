//go:build e2e && dbtls && linux

package api_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"durpdeploy/internal/mssqldriver"
	"durpdeploy/internal/testutil"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const databaseTLSPostgresImage = "harbor.durp.info/dockerhub/library/postgres:16-alpine@" +
	"sha256:1a66d744c1b459e13b05a8fca341da84cb63383e99ce262210efee5a319d4551"

const databaseTLSMSSQLImage = "harbor.durp.info/microsoft/mssql/server:2022-latest@" +
	"sha256:4402d880dd4c34bfa7d8705e56a86cd6c88da80a1f6bbbe741f999e76264a090"

func databaseTLSContainer(
	t *testing.T,
	certificates databaseTLSCertificates,
) string {
	t.Helper()
	backend := os.Getenv("DURPDEPLOY_TLS_BACKEND")
	password := "TLS!" + rand.Text()
	request := testcontainers.ContainerRequest{
		Files: []testcontainers.ContainerFile{
			{
				HostFilePath:      certificates.cert,
				ContainerFilePath: "/tmp/dbtls/server.pem",
				FileMode:          0644,
			},
			{
				HostFilePath:      certificates.key,
				ContainerFilePath: "/tmp/dbtls/server.key",
				FileMode:          0600,
			},
		},
	}
	var port, scheme, user, query string
	switch backend {
	case "postgres":
		port, scheme, user, query = "5432/tcp", "postgres", "postgres", "sslmode=disable"
		request.Image = databaseTLSPostgresImage
		request.Env = map[string]string{
			"POSTGRES_PASSWORD": password,
			"POSTGRES_DB":       "durpdeploy",
		}
		request.Cmd = []string{
			"sh",
			"-c",
			"chown postgres:postgres /tmp/dbtls/server.key; chmod 600 /tmp/dbtls/server.key; exec docker-entrypoint.sh postgres -c ssl=on -c ssl_cert_file=/tmp/dbtls/server.pem -c ssl_key_file=/tmp/dbtls/server.key",
		}
		request.WaitingFor = wait.ForLog("database system is ready to accept connections").
			WithOccurrence(2).
			WithStartupTimeout(3 * time.Minute)
	case "mssql":
		port, scheme, user, query = "1433/tcp", "sqlserver", "sa", "database=master&encrypt=false&trustservercertificate=true"
		request.Image = databaseTLSMSSQLImage
		request.Env = map[string]string{
			"ACCEPT_EULA":       "Y",
			"MSSQL_SA_PASSWORD": password,
		}
		config := filepath.Join(t.TempDir(), "mssql.conf")
		if err := os.WriteFile(config, []byte("[network]\ntlscert = /tmp/dbtls/server.pem\ntlskey = /tmp/dbtls/server.key\nforceencryption = 0\n"), 0600); err != nil {
			t.Fatal(err)
		}
		request.Files = append(request.Files, testcontainers.ContainerFile{
			HostFilePath: config, ContainerFilePath: "/var/opt/mssql/mssql.conf", FileMode: 0644,
		})
		request.User = "0"
		request.Entrypoint = []string{"/bin/bash", "-ec"}
		request.Cmd = []string{
			"chown -R 10001:0 /tmp/dbtls /var/opt/mssql; chmod 600 /tmp/dbtls/server.key; exec su --preserve-environment -s /bin/bash mssql -c 'exec /opt/mssql/bin/sqlservr'",
		}
		request.WaitingFor = wait.ForLog("SQL Server is now ready for client connections").
			WithStartupTimeout(3 * time.Minute)
	default:
		t.Fatal("set DURPDEPLOY_TLS_BACKEND to postgres or mssql")
	}
	request.ExposedPorts = []string{port}
	request.HostConfigModifier = func(config *container.HostConfig) {
		config.PortBindings = network.PortMap{
			network.MustParsePort(port): {
				{HostIP: netip.MustParseAddr("127.0.0.1")},
			},
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	c, err := testcontainers.GenericContainer(
		ctx,
		testcontainers.GenericContainerRequest{
			ContainerRequest: request, Started: true,
		},
	)
	testutil.CleanupContainer(t, c)
	if err != nil {
		t.Fatalf("start database TLS fixture: %v", err)
	}
	inspection, err := c.Inspect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, bindings := range inspection.NetworkSettings.Ports {
		for _, binding := range bindings {
			if binding.HostIP.String() != "127.0.0.1" {
				t.Fatal("database TLS fixture exposes a non-loopback port")
			}
		}
	}
	mapped, err := c.MappedPort(ctx, port)
	if err != nil {
		t.Fatal(err)
	}
	path := ""
	if backend == "postgres" {
		path = "/durpdeploy"
	}
	dsn := (&url.URL{Scheme: scheme, User: url.UserPassword(user, password),
		Host: net.JoinHostPort("localhost", mapped.Port()), Path: path, RawQuery: query}).String()
	if backend == "mssql" {
		probe, err := sql.Open(mssqldriver.DriverName, dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := probe.Close(); err != nil {
				t.Error(err)
			}
		}()
		for {
			if err := probe.PingContext(ctx); err == nil {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("database TLS fixture login readiness timed out")
			case <-time.After(250 * time.Millisecond):
			}
		}
	}
	return dsn
}
