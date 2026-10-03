package agentserver_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"

	"durpdeploy/internal/db"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

type fleetQueryCounter struct {
	db.DBTX
	reads atomic.Int64
}

func (c *fleetQueryCounter) QueryContext(
	ctx context.Context, query string, args ...any,
) (*sql.Rows, error) {
	c.reads.Add(1)
	return c.DBTX.QueryContext(ctx, query, args...)
}

func (c *fleetQueryCounter) QueryRowContext(
	ctx context.Context, query string, args ...any,
) *sql.Row {
	c.reads.Add(1)
	return c.DBTX.QueryRowContext(ctx, query, args...)
}

func TestAgentFleetListMatchesDetailsE2E(t *testing.T) {
	f := newAgentFixture(t)
	srv := fleetAdminServer(t, f)
	current := seedPollPayload(t, f, "pending", "test-agent")
	decodePollResponse(t, postAgent(t, f, agentproto.PollPath, pollBody))
	seedPollPayload(t, f, "pending", "test-agent")
	success := seedPollPayload(t, f, "pending", "test-agent")
	failure := seedPollPayload(t, f, "pending", "test-agent")
	for _, terminal := range []struct {
		id    int64
		state string
	}{{success, "succeeded"}, {failure, "failed"}} {
		if _, err := f.repo.DB.Exec(`UPDATE remote_deployment_claims
			SET state=?, reason=?, finished_at=10, started_at=1,
			claim_token_hash=randomblob(32), ciphertext='fixture',
			claim_expires_at=60, last_heartbeat_at=1 WHERE deployment_id=?`,
			terminal.state, terminal.state, terminal.id); err != nil {
			t.Fatal(err)
		}
		if _, err := f.repo.DB.Exec(`UPDATE deployments
			SET status=?, finished_at=10 WHERE id=?`,
			terminal.state, terminal.id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.repo.DB.Exec(`INSERT INTO agents
		(id,name,endpoint,status,certificate_pem,certificate_fingerprint,encrypted_identity)
		VALUES('second','Second agent','https://second','active',
		'fixture',lower(hex(randomblob(32))),'fixture');
		INSERT INTO agent_interpreters(agent_id,interpreter)
		VALUES('second','python3')`); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		id    int64
		state string
	}{{current, "started"}, {success, "succeeded"}, {failure, "lost"}} {
		if _, err := f.repo.DB.Exec(`INSERT INTO remote_step_runs
			(deployment_id,step_index,agent_id,state,finished_at)
			VALUES(?,1,'second',?,20)`, row.id, row.state); err != nil {
			t.Fatal(err)
		}
	}
	counter := &fleetQueryCounter{DBTX: f.repo.DB}
	f.repo.Queries = db.New(counter)
	readList := func() []map[string]any {
		t.Helper()
		var items []map[string]any
		if err := json.Unmarshal(fleetRequest(t, srv, "GET",
			"/api/v1/admin/agents", "admin", "", 200), &items); err != nil {
			t.Fatal(err)
		}
		return items
	}
	items := readList()
	reads := counter.reads.Load()
	for _, item := range items {
		var detail struct {
			Agent map[string]any `json:"agent"`
		}
		if err := json.Unmarshal(fleetRequest(t, srv, "GET",
			"/api/v1/admin/agents/"+item["id"].(string), "admin", "", 200),
			&detail); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(item, detail.Agent) {
			t.Fatalf(
				"list/detail mismatch: list=%+v detail=%+v",
				item,
				detail.Agent,
			)
		}
		if item["last_successful_deployment"] == nil ||
			item["last_error"] == nil {
			t.Fatalf("history omitted from populated agent: %+v", item)
		}
	}
	for i := range 40 {
		if _, err := f.repo.DB.Exec(`INSERT INTO agents(id,name,endpoint)
			VALUES(?,?,'https://fixture')`, fmt.Sprint(i), fmt.Sprint(i)); err != nil {
			t.Fatal(err)
		}
	}
	counter.reads.Store(0)
	if expanded := readList(); len(expanded) != 42 ||
		counter.reads.Load() != reads {
		t.Fatalf("fleet grew queries: items=%d reads=%d previous=%d",
			len(expanded), counter.reads.Load(), reads)
	}
	t.Logf("fleet list uses %d reads for both 2 and 42 agents", reads)
}
