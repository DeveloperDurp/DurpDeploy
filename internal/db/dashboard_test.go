package db_test

import (
	"context"
	"database/sql"
	"slices"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/testutil"
)

// The dashboard attention rule: failed and expired deployments surface only
// while no later attempt for the same project + environment has taken over.
// A newer attempt whose status is anything but a dead end (cancelled,
// rejected) supersedes the failure: an active attempt hides it, a success
// resolves it, a newer failure collapses it. pending_approval,
// awaiting_artifact_approval and cleanup_unconfirmed always surface;
// queued, pending, running and publishing_artifact belong to the running
// section; succeeded, cancelled and rejected never appear.
func TestListDashboardDeployments(t *testing.T) {
	ctx := context.Background()
	dbConn, err := migrate.Run(":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer testutil.Close(t, dbConn)
	q := db.New(dbConn)

	proj, err := q.CreateProject(ctx, db.CreateProjectParams{Name: "rules"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	env, err := q.CreateEnvironment(ctx, db.CreateEnvironmentParams{
		Name: "prod",
	})
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	other, err := q.CreateEnvironment(ctx, db.CreateEnvironmentParams{
		Name: "dev",
	})
	if err != nil {
		t.Fatalf("create second environment: %v", err)
	}
	rel, err := q.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: proj.ID, Version: "v1", StepsJson: "[]",
	})
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	otherRel, err := q.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: proj.ID, Version: "v2", StepsJson: "[]",
	})
	if err != nil {
		t.Fatalf("create second release: %v", err)
	}

	// Attempts are spaced on distinct seconds so "newer attempt" checks
	// resolve on created_at alone. Rows mutate status like the runner does.
	var tick int64
	next := func(status string) int64 {
		t.Helper()
		tick++
		row, err := q.CreateDeployment(ctx, db.CreateDeploymentParams{
			ReleaseID:     rel.ID,
			EnvironmentID: env.ID,
			Status:        status,
			Forced:        0,
			Note:          sql.NullString{Valid: false},
		})
		if err != nil {
			t.Fatalf("create %s: %v", status, err)
		}
		if _, err := dbConn.ExecContext(ctx,
			"UPDATE deployments SET created_at = ? WHERE id = ?",
			1794100000+tick, row.ID); err != nil {
			t.Fatalf("backdate: %v", err)
		}
		return row.ID
	}
	morph := func(id int64, status string) {
		t.Helper()
		tick++
		if _, err := dbConn.ExecContext(ctx,
			"UPDATE deployments SET status = ?, created_at = ? WHERE id = ?",
			status, 1794100000+tick, id); err != nil {
			t.Fatalf("mutate to %s: %v", status, err)
		}
	}
	attention := []string{}
	want := func(expected []string) {
		t.Helper()
		attention = attention[:0]
		for _, part := range []string{"attention", "active"} {
			rows, err := q.ListDashboardDeployments(ctx,
				db.ListDashboardDeploymentsParams{
					IsAdmin: 1, UserID: 1, Section: part, RowLimit: 50,
				})
			if err != nil {
				t.Fatalf("%s rows: %v", part, err)
			}
			for _, row := range rows {
				attention = append(attention, row.Status)
			}
		}
		slices.Sort(attention)
		slices.Sort(expected)
		if !slices.Equal(attention, expected) {
			t.Fatalf("rows = %v, want %v", attention, expected)
		}
	}

	// Nothing exists: both sections empty.
	if len(attention) != 0 {
		t.Fatalf("empty rows = %v", attention)
	}
	next("running")
	next("failed")
	// Unresolved failure surfaces beside the running deployment; the
	// alone-in-history success does not appear anywhere.
	want([]string{"failed", "running"})
	// A pending re-run takes over: the failure leaves attention and the
	// re-run joins running.
	attempt := next("pending")
	want([]string{"running", "pending"})
	// Cancelling the re-run is a dead end: the old failure resurfaces.
	morph(attempt, "cancelled")
	want([]string{"failed", "running"})
	// Success elsewhere does not resolve it: only the same project +
	// environment pairing counts.
	if _, err := q.CreateDeployment(ctx, db.CreateDeploymentParams{
		ReleaseID:     otherRel.ID,
		EnvironmentID: other.ID,
		Status:        "succeeded",
		Forced:        0,
	}); err != nil {
		t.Fatalf("create other env success: %v", err)
	}
	want([]string{"failed", "running"})
	// A successful retry resolves the failure for good.
	attempt = next("pending")
	want([]string{"running", "pending"})
	morph(attempt, "succeeded")
	want([]string{"running"})
	// Repeated failures collapse to the newest unresolved one.
	next("failed")
	want([]string{"failed", "running"})
	next("failed")
	want([]string{"failed", "running"})
	// Approval states and cleanup_unconfirmed always surface. Each takes
	// over the failure too: the newest unresolved attempt is the item.
	next("pending_approval")
	want([]string{"running", "pending_approval"})
	next("awaiting_artifact_approval")
	want([]string{"running", "pending_approval",
		"awaiting_artifact_approval"})
	next("cleanup_unconfirmed")
	want([]string{"running", "pending_approval",
		"awaiting_artifact_approval", "cleanup_unconfirmed"})
}
