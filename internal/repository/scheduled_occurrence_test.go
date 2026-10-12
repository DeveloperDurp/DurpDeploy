package repository

import (
	"database/sql"
	"errors"
	"sync"
	"testing"

	"durpdeploy/internal/db"
)

func seedOccurrence(t *testing.T, repo *Repository) ScheduledOccurrence {
	t.Helper()
	schedule, err := repo.CreateScheduledDeployment(t.Context(),
		db.CreateScheduledDeploymentParams{
			ProjectID: 1, ReleaseID: 1, EnvironmentID: 1,
			Cron: "* * * * *", NextRunAt: 100, Enabled: 1,
			Note: sql.NullString{String: "observed", Valid: true},
		})
	if err != nil {
		t.Fatal(err)
	}
	return ScheduledOccurrence{Schedule: schedule, NextRunAt: 160, FiredAt: 101}
}

func TestScheduledOccurrenceAtomicityAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		first, second := openDeploymentCreationEngine(t,
			newDeploymentCreationEngine(t, name))
		arg := db.CreateDeploymentParams{
			ReleaseID: 1, EnvironmentID: 1, Status: "pending",
		}
		t.Run("competing duplicate", func(t *testing.T) {
			// Given: two connections observed the same due occurrence.
			occurrence := seedOccurrence(t, first)
			start := make(chan struct{})
			results := make(chan error, 2)
			var workers sync.WaitGroup
			for _, repo := range []*Repository{first, second} {
				workers.Go(func() {
					<-start
					_, err := repo.CreateScheduledOccurrence(
						t.Context(), arg, occurrence)
					results <- err
				})
			}
			// When: both callers try to create that occurrence concurrently.
			close(start)
			workers.Wait()
			close(results)
			// Then: one deployment is created and one schedule advances once.
			succeeded, stale := 0, 0
			for err := range results {
				switch {
				case err == nil:
					succeeded++
				case errors.Is(err, ErrScheduleOccurrenceChanged):
					stale++
				default:
					t.Fatal(err)
				}
			}
			if succeeded != 1 || stale != 1 {
				t.Fatalf("successful=%d stale=%d", succeeded, stale)
			}
			var count int
			if err := first.DB.QueryRowContext(t.Context(),
				"SELECT COUNT(*) FROM deployments").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 1 {
				t.Fatalf("deployments=%d, want 1", count)
			}
			saved, err := first.Queries.GetScheduledDeployment(
				t.Context(), occurrence.Schedule.ID)
			if err != nil {
				t.Fatal(err)
			}
			if saved.NextRunAt != 160 || !saved.LastFiredAt.Valid ||
				saved.LastFiredAt.Int64 != 101 {
				t.Fatal(
					"schedule advancement was not committed with deployment",
				)
			}
		})
		t.Run("creation rollback", func(t *testing.T) {
			// Given: a due schedule whose release snapshot fails decoding.
			occurrence := seedOccurrence(t, first)
			if _, err := first.Queries.UpdateRelease(t.Context(),
				db.UpdateReleaseParams{
					ID: 1, ProjectID: 1, Version: "v1", StepsJson: "{invalid",
				}); err != nil {
				t.Fatal(err)
			}
			// When: creation fails after the transaction advances the occurrence.
			_, err := first.CreateScheduledOccurrence(
				t.Context(),
				arg,
				occurrence,
			)
			// Then: neither the cursor nor last-fired value is committed.
			if err == nil || errors.Is(err, ErrScheduleOccurrenceChanged) {
				t.Fatalf("expected deployment creation failure, got %v", err)
			}
			saved, err := first.Queries.GetScheduledDeployment(
				t.Context(), occurrence.Schedule.ID)
			if err != nil {
				t.Fatal(err)
			}
			if saved.NextRunAt != occurrence.Schedule.NextRunAt ||
				saved.LastFiredAt != occurrence.Schedule.LastFiredAt {
				t.Fatal("failed deployment consumed its schedule occurrence")
			}
			var count int
			if err := first.DB.QueryRowContext(t.Context(),
				"SELECT COUNT(*) FROM deployments").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 1 {
				t.Fatalf(
					"failed creation changed deployment count to %d",
					count,
				)
			}
			if _, err := first.Queries.UpdateRelease(t.Context(),
				db.UpdateReleaseParams{
					ID: 1, ProjectID: 1, Version: "v1", StepsJson: "[]",
				}); err != nil {
				t.Fatal(err)
			}
			if _, err := first.CreateScheduledOccurrence(
				t.Context(), arg, occurrence); err != nil {
				t.Fatalf(
					"restored release could not retry due occurrence: %v",
					err,
				)
			}
		})
		for _, change := range []string{"disabled", "edited note", "edited cron", "new cursor"} {
			t.Run(change, func(t *testing.T) {
				// Given: the stored occurrence changed after it was observed.
				occurrence := seedOccurrence(t, first)
				edit := db.UpdateScheduledDeploymentParams{
					ID: occurrence.Schedule.ID, ProjectID: 1, ReleaseID: 1,
					EnvironmentID: 1, Cron: occurrence.Schedule.Cron,
					NextRunAt: 100, Enabled: 1, Note: occurrence.Schedule.Note,
				}
				switch change {
				case "disabled":
					edit.Enabled = 0
				case "edited note":
					edit.Note.String = "changed"
				case "edited cron":
					edit.Cron = "*/2 * * * *"
				case "new cursor":
					edit.NextRunAt = 120
				}
				if _, err := first.UpdateScheduledDeployment(
					t.Context(), edit); err != nil {
					t.Fatal(err)
				}
				// When: the stale observation is submitted.
				_, err := first.CreateScheduledOccurrence(
					t.Context(),
					arg,
					occurrence,
				)
				// Then: no obsolete deployment is created.
				if !errors.Is(err, ErrScheduleOccurrenceChanged) {
					t.Fatalf("stale occurrence error=%v", err)
				}
				var count int
				if err := first.DB.QueryRowContext(t.Context(),
					"SELECT COUNT(*) FROM deployments").Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count != 2 {
					t.Fatalf(
						"stale occurrence created deployment: count=%d",
						count,
					)
				}
			})
		}
	})
}
