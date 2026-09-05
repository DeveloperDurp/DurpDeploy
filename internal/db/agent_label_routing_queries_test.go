package db_test

import (
	"database/sql"
	"testing"

	"durpdeploy/internal/db"
)

func TestAgentLabelRoutingQueries(t *testing.T) {
	fixture := newAgentLabelRoutingFixture(t)
	ctx := fixture.ctx
	conn := fixture.conn
	queries := fixture.queries
	project := fixture.project
	localProject := fixture.localProject
	environment := fixture.environment
	release := fixture.release
	catFact := fixture.catFact
	build := fixture.build
	members, err := queries.ListEligibleAgentLabelMembers(ctx, catFact.ID)
	if err != nil {
		t.Fatalf("list eligible members: %v", err)
	}
	if len(members) != 2 || members[0].ID != "agent-a" ||
		members[1].ID != "agent-b" {
		t.Fatalf("eligible members = %#v", members)
	}
	t.Logf(
		"labels: %d=%q %d=%q; Cat Fact eligible members: %s,%s",
		catFact.ID,
		catFact.Name,
		build.ID,
		build.Name,
		members[0].ID,
		members[1].ID,
	)
	pending, err := queries.CreatePendingAgent(ctx, db.CreatePendingAgentParams{
		ID: "pending", Name: "Pending",
	})
	if err != nil {
		t.Fatalf("create pending agent: %v", err)
	}
	_, membershipErr := queries.CreateAgentLabelMembership(
		ctx,
		db.CreateAgentLabelMembershipParams{
			AgentLabelID: catFact.ID,
			AgentID:      pending.ID,
		},
	)
	if membershipErr == nil {
		t.Fatal("membership accepted an inactive, unpaired agent")
	}
	t.Logf("inactive membership rejected: %v", membershipErr)

	_, err = queries.CreateProjectExecutionPolicy(
		ctx,
		db.CreateProjectExecutionPolicyParams{
			ProjectID:  localProject.ID,
			TargetMode: "local",
		},
	)
	if err != nil {
		t.Fatalf("create local policy: %v", err)
	}
	_, err = queries.CreateProjectExecutionPolicy(
		ctx,
		db.CreateProjectExecutionPolicyParams{
			ProjectID:     project.ID,
			TargetMode:    "label",
			AgentLabelID:  sql.NullInt64{Int64: catFact.ID, Valid: true},
			AgentStrategy: sql.NullString{String: "all", Valid: true},
		},
	)
	if err != nil {
		t.Fatalf("create label policy: %v", err)
	}
	t.Logf(
		"policies: project %d=local; project %d=label/%s/all",
		localProject.ID,
		project.ID,
		catFact.Name,
	)

	root, err := queries.CreateDeployment(ctx, db.CreateDeploymentParams{
		ReleaseID: release.ID, EnvironmentID: environment.ID, Status: "pending",
	})
	if err != nil {
		t.Fatalf("create root: %v", err)
	}
	_, err = queries.CreateDeploymentRoutingSnapshot(
		ctx,
		db.CreateDeploymentRoutingSnapshotParams{
			DeploymentID:   root.ID,
			Source:         "request",
			TargetMode:     "label",
			AgentLabelID:   sql.NullInt64{Int64: catFact.ID, Valid: true},
			AgentLabelName: sql.NullString{String: catFact.Name, Valid: true},
			AgentStrategy:  sql.NullString{String: "all", Valid: true},
		},
	)
	if err != nil {
		t.Fatalf("create routing snapshot: %v", err)
	}
	for position, member := range members {
		_, err := queries.AddDeploymentRoutingAgent(
			ctx,
			db.AddDeploymentRoutingAgentParams{
				DeploymentID: root.ID,
				Position:     int64(position),
				AgentID:      member.ID,
				AgentName:    member.Name,
			},
		)
		if err != nil {
			t.Fatalf("snapshot member: %v", err)
		}
		_, err = queries.CreateRoutingChildDeployment(
			ctx,
			db.CreateRoutingChildDeploymentParams{
				ReleaseID:          release.ID,
				EnvironmentID:      environment.ID,
				Status:             "pending",
				ParentDeploymentID: sql.NullInt64{Int64: root.ID, Valid: true},
				TargetAgentID:      sql.NullString{String: member.ID, Valid: true},
				TargetAgentName:    sql.NullString{String: member.Name, Valid: true},
			},
		)
		if err != nil {
			t.Fatalf("create child: %v", err)
		}
	}

	children, err := queries.ListDeploymentChildren(
		ctx,
		sql.NullInt64{Int64: root.ID, Valid: true},
	)
	if err != nil {
		t.Fatalf("list children: %v", err)
	}
	if len(children) != 2 {
		t.Fatalf("child count = %d, want 2", len(children))
	}
	roots, err := queries.ListDeployments(ctx)
	if err != nil {
		t.Fatalf("list roots: %v", err)
	}
	if len(roots) != 1 || roots[0].ID != root.ID {
		t.Fatalf("root list = %#v", roots)
	}
	count, err := queries.CountDeploymentsToday(ctx)
	if err != nil {
		t.Fatalf("count roots: %v", err)
	}
	if count != 1 {
		t.Fatalf("root count = %d, want 1", count)
	}
	t.Logf(
		"deployments: root=%d children=%d,%d; standard list=%d root",
		root.ID,
		children[0].ID,
		children[1].ID,
		count,
	)
	cursor, err := queries.CreateAgentLabelCursor(
		ctx,
		db.CreateAgentLabelCursorParams{AgentLabelID: catFact.ID},
	)
	if err != nil {
		t.Fatalf("create cursor: %v", err)
	}
	cursor, err = queries.AdvanceAgentLabelCursor(
		ctx,
		db.AdvanceAgentLabelCursorParams{
			AgentLabelID: catFact.ID,
			LastAgentID:  sql.NullString{String: "agent-b", Valid: true},
		},
	)
	if err != nil || cursor.LastAgentID.String != "agent-b" {
		t.Fatalf("advance cursor = %#v, %v", cursor, err)
	}
	schedule, err := queries.CreateScheduledDeployment(
		ctx,
		db.CreateScheduledDeploymentParams{
			ProjectID: project.ID, ReleaseID: release.ID,
			EnvironmentID: environment.ID, Cron: "0 * * * *",
			NextRunAt: 100, Enabled: 1,
		},
	)
	if err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	occurrence := db.ClaimScheduledDeploymentOccurrenceParams{
		ScheduledDeploymentID: schedule.ID,
		DueAt:                 100,
		DeploymentID:          root.ID,
	}
	if _, err := queries.ClaimScheduledDeploymentOccurrence(ctx, occurrence); err != nil {
		t.Fatalf("claim occurrence: %v", err)
	}
	_, duplicateOccurrenceErr := queries.ClaimScheduledDeploymentOccurrence(
		ctx,
		occurrence,
	)
	if duplicateOccurrenceErr == nil {
		t.Fatal("duplicate scheduled occurrence succeeded")
	}
	if _, err := queries.DeleteAgentPairing(ctx, "agent-a"); err != nil {
		t.Fatalf("delete pairing before agent: %v", err)
	}
	deleted, err := queries.DeleteAgent(ctx, "agent-a")
	if err != nil || deleted != 1 {
		t.Fatalf("delete snapshotted agent = %d, %v", deleted, err)
	}
	preservedAgents, err := queries.ListDeploymentRoutingAgents(ctx, root.ID)
	if err != nil || len(preservedAgents) != 2 {
		t.Fatalf("preserved routing agents = %#v, %v", preservedAgents, err)
	}
	_, deleteLabelErr := queries.DeleteAgentLabel(ctx, catFact.ID)
	if deleteLabelErr == nil {
		t.Fatal("label deletion ignored routing references")
	}
	t.Logf(
		"constraint failures: occurrence=%q label-delete=%q; deleted agent retained %d snapshot identities",
		duplicateOccurrenceErr,
		deleteLabelErr,
		len(preservedAgents),
	)

	_, nestedErr := conn.ExecContext(ctx, `
UPDATE deployments SET parent_deployment_id = ? WHERE id = ?
`, children[0].ID, root.ID)
	if nestedErr == nil {
		t.Fatal("nested parent update succeeded")
	}
	_, selfErr := conn.ExecContext(ctx, `
UPDATE deployments SET parent_deployment_id = id WHERE id = ?
`, children[0].ID)
	if selfErr == nil {
		t.Fatal("self-parent update succeeded")
	}
	t.Logf("constraint failures: nested=%q self=%q", nestedErr, selfErr)
}
