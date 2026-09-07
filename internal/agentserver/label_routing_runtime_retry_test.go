package agentserver

import (
	"database/sql"
	"net/http"
	"reflect"
	"strconv"
	"testing"

	"durpdeploy/internal/db"
)

func (f *labelRuntimeFixture) exactRetry(t *testing.T) db.Deployment {
	t.Helper()
	f.requireApproval(t)
	source, err := f.repo.Queries.CreateDeployment(t.Context(),
		db.CreateDeploymentParams{
			ReleaseID: f.release.ID, EnvironmentID: f.environment.ID,
			Status: "failed",
		})
	runtimeMust(t, err)
	_, err = f.repo.Queries.CreateDeploymentRoutingSnapshot(t.Context(),
		db.CreateDeploymentRoutingSnapshotParams{
			DeploymentID: source.ID, Source: "request", TargetMode: "label",
			AgentLabelID:   sql.NullInt64{Int64: f.label.ID, Valid: true},
			AgentLabelName: sql.NullString{String: f.label.Name, Valid: true},
			AgentStrategy:  sql.NullString{String: "all", Valid: true},
		})
	runtimeMust(t, err)
	for position, id := range []string{f.agents[1], f.agents[0]} {
		_, err := f.repo.Queries.AddDeploymentRoutingAgent(t.Context(),
			db.AddDeploymentRoutingAgentParams{
				DeploymentID: source.ID, Position: int64(position),
				AgentID: id, AgentName: id,
			})
		runtimeMust(t, err)
	}
	retry, err := f.service(f.repo).Retry(t.Context(), source)
	runtimeMust(t, err)
	_, err = f.repo.DB.ExecContext(
		t.Context(),
		"DELETE FROM agent_label_memberships WHERE agent_label_id=?",
		f.label.ID,
	)
	runtimeMust(t, err)
	_, err = f.repo.Queries.CreateAgentLabelMembership(t.Context(),
		db.CreateAgentLabelMembershipParams{
			AgentLabelID: f.label.ID, AgentID: f.agents[2],
		})
	runtimeMust(t, err)
	f.assertAgents(t, retry.ID, []string{f.agents[1], f.agents[0]})
	return retry
}

func runtimeExactApprovalCAS(t *testing.T, f *labelRuntimeFixture) {
	root := f.exactRetry(t)
	f.approvalCAS(t, root.ID)
	f.assertAgents(t, root.ID, []string{f.agents[1], f.agents[0]})
	f.assertExactChildren(t, root.ID)
	if got := f.routingCounts(t); !reflect.DeepEqual(
		got,
		[]int{4, 2, 4, 2, 1, 0},
	) {
		t.Fatalf("exact approval counts=%v", got)
	}
}

func runtimeExactApprovalRollback(t *testing.T, f *labelRuntimeFixture) {
	root := f.exactRetry(t)
	before := f.routingCounts(t)
	intent, err := f.repo.Queries.GetDeploymentRoutingSnapshot(
		t.Context(),
		root.ID,
	)
	runtimeMust(t, err)
	_, err = f.repo.DB.ExecContext(t.Context(),
		"UPDATE agents SET status='disabled' WHERE id=?", f.agents[1])
	runtimeMust(t, err)
	server := f.approvalServer(t, f.connection(t))
	url := server.URL + "/api/v1/deployments/" + strconv.FormatInt(
		root.ID,
		10,
	) + "/approve"
	response := runtimeApprove(url)
	runtimeMust(t, response.err)
	t.Logf("HTTP unavailable exact-set approval status=%d", response.status)
	if response.status != http.StatusConflict {
		t.Fatalf(
			"exact unavailable status=%d body=%s",
			response.status,
			response.body,
		)
	}
	stored, err := f.repo.Queries.GetDeployment(t.Context(), root.ID)
	runtimeMust(t, err)
	storedIntent, err := f.repo.Queries.GetDeploymentRoutingSnapshot(
		t.Context(),
		root.ID,
	)
	runtimeMust(t, err)
	if stored != root || intent != storedIntent ||
		!reflect.DeepEqual(before, f.routingCounts(t)) {
		t.Fatal("unavailable exact approval mutated pending root or intent")
	}
	f.assertAgents(t, root.ID, []string{f.agents[1], f.agents[0]})
	_, err = f.repo.DB.ExecContext(
		t.Context(),
		"UPDATE agents SET status='active', name='renamed' WHERE id=?",
		f.agents[1],
	)
	runtimeMust(t, err)
	response = runtimeApprove(url)
	runtimeMust(t, response.err)
	if response.status != http.StatusOK {
		t.Fatalf(
			"reactivated exact approval=%d body=%s",
			response.status,
			response.body,
		)
	}
	f.assertAgents(t, root.ID, []string{f.agents[1], f.agents[0]})
	f.assertExactChildren(t, root.ID)
}

func (f *labelRuntimeFixture) assertExactChildren(t *testing.T, id int64) {
	t.Helper()
	children, err := f.repo.Queries.ListDeploymentChildren(
		t.Context(),
		sql.NullInt64{Int64: id, Valid: true},
	)
	runtimeMust(t, err)
	if len(children) != 2 {
		t.Fatalf("exact children=%d want=2", len(children))
	}
	for i, agent := range []string{f.agents[1], f.agents[0]} {
		if children[i].TargetAgentID.String != agent ||
			children[i].TargetAgentName.String != agent {
			t.Fatalf(
				"exact child[%d]=%v want copied identity=%s",
				i,
				children[i],
				agent,
			)
		}
	}
}
