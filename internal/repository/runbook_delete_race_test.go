package repository

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"durpdeploy/internal/db"
)

func TestRunbookDeletionConcurrentAdmissionAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		first, second := openDeploymentCreationEngine(
			t, newDeploymentCreationEngine(t, name),
		)
		for attempt := range 8 {
			assertRunbookAdmissionRace(t, first, second, attempt)
		}
	})
}

func assertRunbookAdmissionRace(
	t *testing.T, first, second *Repository, attempt int,
) {
	t.Helper()
	// Given: two independent connections and a live runbook.
	book, _, err := first.SaveRunbook(t.Context(), RunbookSave{
		ProjectID: 1, Name: fmt.Sprintf("race-%d", attempt), StepsJSON: "[]",
	})
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	executed := make(chan error, 1)
	deleted := make(chan error, 1)
	// When: deletion and execution admission race.
	go func() {
		<-start
		_, _, err := first.CreateRunbookExecution(
			t.Context(),
			RunbookExecutionRequest{
				ProjectID: 1, RunbookID: book.ID, EnvironmentID: 1,
			},
		)
		executed <- err
	}()
	go func() {
		<-start
		_, err := second.DeleteRunbook(t.Context(), db.GetRunbookParams{
			ID: book.ID, ProjectID: 1,
		})
		deleted <- err
	}()
	close(start)
	executionErr, deletionErr := <-executed, <-deleted
	// Then: exactly one operation wins, with no hidden execution.
	if executionErr == nil {
		if !errors.Is(deletionErr, ErrRunbookHasActiveExecution) {
			t.Fatalf("execution won but deletion=%v", deletionErr)
		}
	} else if !errors.Is(executionErr, sql.ErrNoRows) || deletionErr != nil {
		t.Fatalf("execution=%v deletion=%v", executionErr, deletionErr)
	}
	var count int
	if err := first.DB.QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM runbook_executions x
JOIN runbook_versions v ON v.id = x.runbook_version_id WHERE v.runbook_id = ?`,
		book.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if deletionErr == nil && count != 0 ||
		executionErr == nil && count != 1 {
		t.Fatalf(
			"execution count=%d execution=%v deletion=%v",
			count,
			executionErr,
			deletionErr,
		)
	}
}
