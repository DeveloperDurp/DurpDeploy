package agentserver

import (
	"database/sql"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"testing"

	"durpdeploy/internal/db"
)

func runtimeRoundRobin(t *testing.T, f *labelRuntimeFixture) {
	// Given three eligible agents and three independent database connections.
	for cycle := range 3 {
		start := make(chan struct{})
		results := make(chan db.Deployment, 3)
		failures := make(chan error, 3)
		var group sync.WaitGroup
		for range 3 {
			service := f.service(f.connection(t))
			group.Add(1)
			go func() {
				defer group.Done()
				<-start
				deployment, err := service.Create(
					t.Context(),
					f.request("round_robin"),
				)
				results <- deployment
				failures <- err
			}()
		}
		// When each cycle races exactly three immediate deployments.
		close(start)
		group.Wait()
		close(results)
		close(failures)
		for err := range failures {
			runtimeMust(t, err)
		}
		// Then each committed cycle contains each target exactly once.
		var selected []string
		for deployment := range results {
			agents, err := f.repo.Queries.ListDeploymentRoutingAgents(
				t.Context(), deployment.ID)
			runtimeMust(t, err)
			if len(agents) != 1 {
				t.Fatalf("routing agents=%v, want one", agents)
			}
			selected = append(selected, agents[0].AgentID)
		}
		sort.Strings(selected)
		if !reflect.DeepEqual(selected, f.agents) {
			t.Fatalf("cycle=%d selected=%v want=%v", cycle, selected, f.agents)
		}
		cursor, err := f.repo.Queries.GetAgentLabelCursor(
			t.Context(),
			f.label.ID,
		)
		runtimeMust(t, err)
		if cursor.LastAgentID.String != f.agents[2] {
			t.Fatalf(
				"cycle cursor=%s want=%s",
				cursor.LastAgentID.String,
				f.agents[2],
			)
		}
		t.Logf(
			"cycle=%d unique_targets=%v cursor=%s",
			cycle,
			selected,
			cursor.LastAgentID.String,
		)
	}
	for i := range 4 {
		deployment := f.create(t, "round_robin")
		agents, err := f.repo.Queries.ListDeploymentRoutingAgents(
			t.Context(),
			deployment.ID,
		)
		runtimeMust(t, err)
		if len(agents) != 1 || agents[0].AgentID != f.agents[i%3] {
			t.Fatalf("sequential turn=%d agents=%v", i, agents)
		}
	}
	if got := f.routingCounts(t); !reflect.DeepEqual(
		got,
		[]int{13, 13, 13, 13, 0, 1},
	) {
		t.Fatalf("round robin counts=%v", got)
	}
}

func runtimeFanout(t *testing.T, f *labelRuntimeFixture) {
	// Given the real all-agent creation boundary.
	root := f.create(t, "all")
	// When recovery is repeated through a fresh connection.
	runtimeMust(
		t,
		f.service(f.connection(t)).DispatchFrozen(t.Context(), root.ID),
	)
	// Then each member has one unique child, payload and dispatch.
	children, err := f.repo.Queries.ListDeploymentChildren(
		t.Context(),
		sql.NullInt64{Int64: root.ID, Valid: true},
	)
	runtimeMust(t, err)
	if len(children) != 3 {
		t.Fatalf("children=%d want=3", len(children))
	}
	seen := map[string]bool{}
	for _, child := range children {
		id := child.TargetAgentID.String
		if seen[id] || child.ParentDeploymentID.Int64 != root.ID {
			t.Fatalf("duplicate target or wrong parent: %v", child)
		}
		seen[id] = true
		dispatched, err := f.repo.Queries.GetDeploymentDispatch(
			t.Context(),
			child.ID,
		)
		runtimeMust(t, err)
		payload, err := f.repo.Queries.GetDeploymentPayload(
			t.Context(),
			child.ID,
		)
		runtimeMust(t, err)
		if dispatched.AssignedAgentID.String != id || payload.Ciphertext == "" {
			t.Fatalf("child=%d missing target or encrypted payload", child.ID)
		}
	}
	for _, id := range f.agents {
		if !seen[id] {
			t.Fatalf("missing target=%s", id)
		}
	}
	if got := f.routingCounts(t); !reflect.DeepEqual(
		got,
		[]int{4, 1, 3, 3, 0, 0},
	) {
		t.Fatalf("fanout counts=%v", got)
	}
}

func runtimeParentConstraints(t *testing.T, f *labelRuntimeFixture) {
	// Given a real fan-out root and its children.
	root := f.create(t, "all")
	children, err := f.repo.Queries.ListDeploymentChildren(
		t.Context(),
		sql.NullInt64{Int64: root.ID, Valid: true},
	)
	runtimeMust(t, err)
	other := f.create(t, "all")
	// When malformed parent or copied-identity rows are submitted directly.
	invalid := []string{
		fmt.Sprintf(
			"UPDATE deployments SET parent_deployment_id=id WHERE id=%d",
			root.ID,
		),
		fmt.Sprintf(
			"UPDATE deployments SET parent_deployment_id=%d WHERE id=%d",
			children[0].ID,
			children[1].ID,
		),
		fmt.Sprintf(
			"UPDATE deployments SET parent_deployment_id=%d WHERE id=%d",
			other.ID,
			root.ID,
		),
		fmt.Sprintf(
			"UPDATE deployments SET target_agent_name=NULL WHERE id=%d",
			children[0].ID,
		),
		fmt.Sprintf(
			"UPDATE deployments SET target_agent_id=NULL WHERE id=%d",
			children[0].ID,
		),
		fmt.Sprintf(
			"UPDATE deployments SET parent_deployment_id=-1 WHERE id=%d",
			children[0].ID,
		),
	}
	for i, statement := range invalid {
		_, err := f.repo.DB.ExecContext(t.Context(), statement)
		// Then constraints reject each write, preserving both roots.
		if err == nil {
			t.Fatalf("malformed parent constraint case=%d accepted", i)
		}
		t.Logf("malformed_parent_case=%d rejected=true", i)
	}
	stored, err := f.repo.Queries.GetDeployment(t.Context(), root.ID)
	runtimeMust(t, err)
	if stored.ParentDeploymentID.Valid {
		t.Fatal("root was changed by rejected parent mutation")
	}
	updated, err := f.repo.Queries.UpdateDeployment(
		t.Context(),
		db.UpdateDeploymentParams{
			ID: stored.ID, ReleaseID: stored.ReleaseID, EnvironmentID: stored.EnvironmentID,
			Status: stored.Status, StartedAt: stored.StartedAt, FinishedAt: stored.FinishedAt,
			Note: sql.NullString{String: "runtime update", Valid: true},
		},
	)
	runtimeMust(t, err)
	if updated.ID != stored.ID || updated.Note.String != "runtime update" {
		t.Fatal("trigger-safe update returned the wrong deployment")
	}
	if got := f.routingCounts(t); !reflect.DeepEqual(
		got,
		[]int{8, 2, 6, 6, 0, 0},
	) {
		t.Fatalf("constraint rollback counts=%v", got)
	}
}
