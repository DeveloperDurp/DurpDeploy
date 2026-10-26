package repository

import (
	"database/sql"
	"errors"
	"testing"

	"durpdeploy/internal/db"
)

func TestScheduledTransitionsAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		first, second := openDeploymentCreationEngine(t,
			newDeploymentCreationEngine(t, name))
		target, err := first.Queries.CreateRelease(
			t.Context(),
			db.CreateReleaseParams{
				ProjectID: 1, Version: "edited", StepsJson: "[]",
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		env, err := first.CreateEnvironment(t.Context(),
			db.CreateEnvironmentParams{Name: "edited"})
		if err != nil {
			t.Fatal(err)
		}
		for _, transition := range []string{"skip", "park", "disable"} {
			for _, edit := range []string{
				"unchanged", "cron", "release", "environment", "note",
				"cursor", "disabled", "deleted",
			} {
				t.Run(transition+"/"+edit, func(t *testing.T) {
					checkScheduledTransition(
						t,
						first,
						second,
						target.ID,
						env.ID,
						transition,
						edit,
					)
				})
			}
		}
	})
}

func checkScheduledTransition(
	t *testing.T,
	first, second *Repository,
	releaseID, environmentID int64,
	transition, edit string,
) {
	t.Helper()
	// Given: a due row read before another connection edits it.
	row := seedOccurrence(t, first).Schedule
	row, err := first.UpdateScheduledDeployment(t.Context(),
		db.UpdateScheduledDeploymentParams{
			ID: row.ID, ProjectID: row.ProjectID,
			ReleaseID: row.ReleaseID, EnvironmentID: row.EnvironmentID,
			Cron: row.Cron, NextRunAt: row.NextRunAt, Enabled: 1,
			Note:        row.Note,
			LastFiredAt: sql.NullInt64{Int64: 90, Valid: true},
		})
	if err != nil {
		t.Fatal(err)
	}
	updated := row
	switch transition {
	case "skip":
		updated.NextRunAt = 160
	case "park":
		updated.NextRunAt = 315360100
	case "disable":
		updated.Enabled = 0
		updated.LastError = "invalid release"
	}
	editScheduleBeforeTransition(t, second, row, releaseID, environmentID, edit)
	before, beforeErr := second.Queries.GetScheduledDeployment(
		t.Context(),
		row.ID,
	)
	// When: the old observation tries to skip, park, or disable.
	err = first.UpdateScheduledOccurrence(
		t.Context(),
		row,
		updated,
	)
	saved, savedErr := second.Queries.GetScheduledDeployment(
		t.Context(),
		row.ID,
	)
	// Then: only an unchanged observation can update the state.
	if edit == "unchanged" {
		if err != nil || savedErr != nil ||
			saved.NextRunAt != updated.NextRunAt ||
			saved.LastFiredAt != updated.LastFiredAt ||
			saved.Enabled != updated.Enabled || saved.LastError != updated.LastError {
			t.Fatalf(
				"transition: saved=%+v error=%v read=%v",
				saved,
				err,
				savedErr,
			)
		}
	} else if !errors.Is(err, ErrScheduleOccurrenceChanged) {
		t.Fatalf("stale transition error=%v", err)
	} else if edit == "deleted" {
		if !errors.Is(savedErr, sql.ErrNoRows) {
			t.Fatalf("deleted row restored: %v", savedErr)
		}
	} else if beforeErr != nil || savedErr != nil || before != saved {
		t.Fatalf("edit changed: before=%+v after=%+v errors=%v/%v", before, saved, beforeErr, savedErr)
	}
}

func editScheduleBeforeTransition(
	t *testing.T,
	second *Repository,
	row db.ScheduledDeployment,
	releaseID, environmentID int64,
	edit string,
) {
	t.Helper()
	change := db.UpdateScheduledDeploymentParams{
		ID: row.ID, ProjectID: row.ProjectID, ReleaseID: row.ReleaseID,
		EnvironmentID: row.EnvironmentID, Cron: row.Cron,
		NextRunAt: row.NextRunAt, Enabled: 1,
		LastFiredAt: row.LastFiredAt, Note: row.Note,
	}
	switch edit {
	case "cron":
		change.Cron = "*/2 * * * *"
	case "release":
		change.ReleaseID = releaseID
	case "environment":
		change.EnvironmentID = environmentID
	case "note":
		change.Note.String = "edited"
	case "cursor":
		change.NextRunAt = 120
	case "disabled":
		change.Enabled = 0
	}
	if edit == "deleted" {
		if err := second.Queries.DeleteScheduledDeployment(t.Context(), row.ID); err != nil {
			t.Fatal(err)
		}
	} else if edit != "unchanged" {
		if _, err := second.UpdateScheduledDeployment(t.Context(), change); err != nil {
			t.Fatal(err)
		}
	}
}
