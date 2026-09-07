package agentserver

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/dispatch"
	"durpdeploy/internal/mssqldriver"
	"durpdeploy/internal/pgdriver"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/secret"
)

type labelRuntimeFixture struct {
	repo        *repository.Repository
	engine, dsn string
	project     db.Project
	environment db.Environment
	release     db.Release
	label       db.AgentLabel
	agents      []string
	box         *secret.Box
}

func newLabelRuntimeFixture(
	t *testing.T, engine, dsn string,
) *labelRuntimeFixture {
	t.Helper()
	f := &labelRuntimeFixture{engine: engine, dsn: dsn}
	f.repo = f.connection(t)
	name := strings.TrimPrefix(t.Name(), "TestAgentLabelRoutingRuntimeParity/")
	var err error
	f.box, err = secret.NewBox(make([]byte, 32))
	runtimeMust(t, err)
	f.project, err = f.repo.Queries.CreateProject(t.Context(),
		db.CreateProjectParams{Name: name})
	runtimeMust(t, err)
	f.environment, err = f.repo.Queries.CreateEnvironment(t.Context(),
		db.CreateEnvironmentParams{Name: name})
	runtimeMust(t, err)
	f.release, err = f.repo.Queries.CreateRelease(t.Context(),
		db.CreateReleaseParams{
			ProjectID: f.project.ID, Version: "v1", StepsJson: "[]",
		})
	runtimeMust(t, err)
	f.label, err = f.repo.Queries.CreateAgentLabel(t.Context(),
		db.CreateAgentLabelParams{
			Name: name, NormalizedName: strings.ToLower(name),
		})
	runtimeMust(t, err)
	for i := range 3 {
		id := fmt.Sprintf("%s-%c", name, 'a'+i)
		f.addAgent(t, id)
		f.agents = append(f.agents, id)
	}
	eligible, err := f.repo.Queries.ListEligibleAgentLabelMembers(
		t.Context(), f.label.ID)
	runtimeMust(t, err)
	if len(eligible) != 3 {
		t.Fatalf("eligible agents=%d, want 3", len(eligible))
	}
	t.Logf("engine=%s eligible_agents=3 separate_connection_pool=true", engine)
	return f
}

func (f *labelRuntimeFixture) connection(t *testing.T) *repository.Repository {
	t.Helper()
	driver := pgdriver.DriverName
	if f.engine == "mssql" {
		driver = mssqldriver.DriverName
	}
	conn, err := sql.Open(driver, f.dsn)
	runtimeMust(t, err)
	conn.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close runtime connection: %v", err)
		}
	})
	runtimeMust(t, conn.PingContext(t.Context()))
	return repository.New(conn)
}

func (f *labelRuntimeFixture) addAgent(t *testing.T, id string) {
	t.Helper()
	hash := sha256.Sum256([]byte(id))
	pin := fmt.Sprintf("%x", hash)
	_, err := f.repo.DB.ExecContext(t.Context(), `
INSERT INTO agents
 (id, name, status, certificate_pem, certificate_fingerprint)
VALUES (?, ?, 'active', 'fixture', ?)`, id, id, pin)
	runtimeMust(t, err)
	_, err = f.repo.DB.ExecContext(t.Context(), `
INSERT INTO agent_pairings
 (agent_id, pairing_code_hash, agent_public_identity, agent_pin,
 server_public_identity, server_pin, state, expires_at, paired_at)
VALUES (?, ?, ?, ?, 'server', ?, 'paired', 1, 1)`,
		id, hash[:], id, pin, pin)
	runtimeMust(t, err)
	_, err = f.repo.Queries.CreateAgentLabelMembership(t.Context(),
		db.CreateAgentLabelMembershipParams{
			AgentLabelID: f.label.ID, AgentID: id,
		})
	runtimeMust(t, err)
}

func (f *labelRuntimeFixture) service(
	repo *repository.Repository,
) *dispatch.CreationService {
	return dispatch.NewCreationService(repo, dispatch.New(repo, f.box, nil))
}

func (f *labelRuntimeFixture) request(strategy string) dispatch.CreateRequest {
	return dispatch.CreateRequest{
		ProjectID: f.project.ID, ReleaseID: f.release.ID,
		EnvironmentID: f.environment.ID,
		Routing: dispatch.Input{
			Source: dispatch.SourceRequest, Mode: "label",
			LabelID: f.label.ID, Strategy: strategy,
		},
	}
}

func (f *labelRuntimeFixture) create(
	t *testing.T, strategy string,
) db.Deployment {
	t.Helper()
	deployment, err := f.service(f.repo).
		Create(t.Context(), f.request(strategy))
	runtimeMust(t, err)
	return deployment
}

func runtimeMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func (f *labelRuntimeFixture) count(t *testing.T, query string) int {
	t.Helper()
	var count int
	runtimeMust(t, f.repo.DB.QueryRowContext(t.Context(), query,
		f.release.ID).Scan(&count))
	return count
}

func (f *labelRuntimeFixture) routingCounts(t *testing.T) []int {
	t.Helper()
	counts := []int{f.count(t,
		"SELECT COUNT(*) FROM deployments WHERE release_id = ?")}
	for _, table := range []string{
		"deployment_routing_snapshots", "deployment_routing_agents",
		"deployment_dispatches", "deployment_approvals",
	} {
		counts = append(counts, f.count(t, "SELECT COUNT(*) FROM "+table+
			" x JOIN deployments d ON d.id=x.deployment_id WHERE d.release_id=?"))
	}
	var cursor int
	runtimeMust(t, f.repo.DB.QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM agent_label_cursors WHERE agent_label_id=?",
		f.label.ID).Scan(&cursor))
	counts = append(counts, cursor)
	payloads := f.count(t, "SELECT COUNT(*) FROM deployment_payloads p "+
		"JOIN deployments d ON d.id=p.deployment_id WHERE d.release_id=?")
	if payloads != counts[3] {
		t.Fatalf("payload count=%d dispatch count=%d", payloads, counts[3])
	}
	t.Logf("SQL encrypted_payload_count=%d", payloads)
	t.Logf(
		"SQL counts [deployments snapshots agents dispatches approvals cursors]=%v",
		counts,
	)
	return counts
}
