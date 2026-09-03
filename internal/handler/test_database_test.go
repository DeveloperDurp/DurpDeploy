package handler_test

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
)

var (
	handlerTemplateOnce sync.Once
	handlerTemplateDir  string
	handlerTemplatePath string
	handlerTemplateErr  error
)

func TestMain(m *testing.M) {
	status := m.Run()
	if handlerTemplateDir != "" {
		_ = os.RemoveAll(handlerTemplateDir)
	}
	os.Exit(status)
}

func newHandlerTestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	template, err := handlerSchemaTemplate()
	if err != nil {
		t.Fatalf("create schema template: %v", err)
	}
	databasePath := filepath.Join(t.TempDir(), "test.db")
	if err := copyHandlerSchema(template, databasePath); err != nil {
		t.Fatalf("copy schema template: %v", err)
	}
	dsn := fmt.Sprintf(
		"file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&"+
			"_pragma=busy_timeout(5000)&_txlock=immediate",
		databasePath,
	)
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open fixture database: %v", err)
	}
	if err := conn.Ping(); err != nil {
		_ = conn.Close()
		t.Fatalf("ping fixture database: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func handlerSchemaTemplate() (string, error) {
	handlerTemplateOnce.Do(func() {
		handlerTemplateDir, handlerTemplateErr = os.MkdirTemp(
			"",
			"durpdeploy-handler-schema-",
		)
		if handlerTemplateErr != nil {
			return
		}
		handlerTemplatePath = filepath.Join(handlerTemplateDir, "schema.db")
		conn, err := migrate.Run(
			"file:" + handlerTemplatePath + "?_pragma=foreign_keys(1)",
		)
		if err != nil {
			handlerTemplateErr = err
			return
		}
		handlerTemplateErr = conn.Close()
	})
	return handlerTemplatePath, handlerTemplateErr
}

func copyHandlerSchema(sourcePath, targetPath string) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()
	target, err := os.Create(targetPath)
	if err != nil {
		return err
	}
	if _, err := io.Copy(target, source); err != nil {
		_ = target.Close()
		return err
	}
	return target.Close()
}

func TestNewHandlerTestDatabase_isolatesFixtures(t *testing.T) {
	// Given
	first := newHandlerTestDatabase(t)
	if _, err := db.New(first).CreateProject(
		context.Background(),
		db.CreateProjectParams{Name: "first"},
	); err != nil {
		t.Fatalf("create project in first fixture: %v", err)
	}

	// When
	second := newHandlerTestDatabase(t)
	projects, err := db.New(second).ListProjects(context.Background())

	// Then
	if err != nil || len(projects) != 0 {
		t.Fatalf(
			"second fixture projects=%d err=%v, want empty",
			len(projects),
			err,
		)
	}
}
