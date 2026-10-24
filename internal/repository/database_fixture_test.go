package repository

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"durpdeploy/internal/testenv"
	"durpdeploy/internal/testutil"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

type repositoryDatabaseServer struct {
	once      sync.Once
	container testcontainers.Container
	admin     *sql.DB
	dsn       string
	err       error
}

var repositoryPostgres, repositorySQLServer repositoryDatabaseServer

func TestMain(m *testing.M) {
	status := testenv.Run(m)
	for _, server := range []*repositoryDatabaseServer{
		&repositoryPostgres, &repositorySQLServer,
	} {
		if server.admin != nil {
			if err := server.admin.Close(); err != nil {
				fmt.Fprintln(os.Stderr, "close repository fixture admin:", err)
				status = 1
			}
		}
		if err := testutil.TerminateContainer(server.container); err != nil {
			fmt.Fprintln(os.Stderr, "remove repository fixture server:", err)
			status = 1
		}
	}
	os.Exit(status)
}

func (server *repositoryDatabaseServer) start(name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	driver := "sqlserver"
	var err error
	if name == "PostgreSQL" {
		driver = "pgx"
		var pg *postgres.PostgresContainer
		pg, err = postgres.Run(ctx,
			"harbor.durp.info/dockerhub/library/postgres:16-alpine",
			postgres.WithDatabase("postgres"),
			postgres.WithUsername("repository"),
			postgres.WithPassword(rand.Text()),
			postgres.BasicWaitStrategies(),
		)
		server.container = pg
		if err == nil {
			server.dsn, err = pg.ConnectionString(ctx, "sslmode=disable")
		}
	} else {
		_, server.dsn, err = testutil.StartSQLServer(ctx,
			"harbor.durp.info/microsoft/mssql/server:2022-latest",
			func(c testcontainers.Container) { server.container = c },
		)
	}
	if err != nil {
		return err
	}
	server.admin, err = sql.Open(driver, server.dsn)
	if err != nil {
		return err
	}
	// Serialize database administration without limiting test connections.
	server.admin.SetMaxOpenConns(1)
	return server.admin.PingContext(ctx)
}

// RepositoryDatabaseDSN gives each case an empty database on its package server.
// It is exported only in the test build for the external repository tests.
func RepositoryDatabaseDSN(t *testing.T, engine string) string {
	t.Helper()
	server := &repositoryPostgres
	switch engine {
	case "PostgreSQL":
	case "SQLServer":
		server = &repositorySQLServer
	default:
		t.Fatalf("unknown shared database engine %q", engine)
	}
	server.once.Do(func() { server.err = server.start(engine) })
	if server.err != nil {
		t.Fatalf("start shared %s fixture: %v", engine, server.err)
	}
	database := "repo_" + strings.ToLower(rand.Text())
	quoted := `"` + database + `"`
	if engine == "SQLServer" {
		quoted = "[" + database + "]"
	}
	// Register before CREATE so an interrupted setup also removes its database.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if _, err := server.admin.ExecContext(ctx,
			"DROP DATABASE IF EXISTS "+quoted); err != nil {
			t.Errorf("remove %s fixture database: %v", engine, err)
		}
	})
	if _, err := server.admin.ExecContext(t.Context(),
		"CREATE DATABASE "+quoted); err != nil {
		t.Fatalf("create %s fixture database: %v", engine, err)
	}
	dsn, err := url.Parse(server.dsn)
	if err != nil {
		t.Fatal(err)
	}
	if engine == "PostgreSQL" {
		dsn.Path = "/" + database
	} else {
		query := dsn.Query()
		query.Set("database", database)
		dsn.RawQuery = query.Encode()
	}
	return dsn.String()
}
