package agentserver

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"durpdeploy/internal/agentpairing"
	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/secret"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
)

func TestPostgres_RemoteAgentRuntimeParity(t *testing.T) {
	ctx := context.Background()
	container, err := postgres.Run(
		ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("durpdeploy"),
		postgres.WithUsername("durpdeploy"),
		postgres.WithPassword("postgres"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Skipf("PostgreSQL runtime parity unavailable: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	testRemoteAgentRuntimeParity(t, dsn)
}

func TestMSSQL_RemoteAgentRuntimeParity(t *testing.T) {
	conn := mssqlRuntimeDB(t)
	testRemoteAgentRuntimeParity(t, conn)
}

func mssqlRuntimeDB(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	const password = "Durpdeploy12345!"
	container, err := testcontainers.GenericContainer(
		ctx,
		testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Image:        "mcr.microsoft.com/mssql/server:2022-latest",
				ExposedPorts: []string{"1433/tcp"},
				Env: map[string]string{
					"ACCEPT_EULA":       "Y",
					"MSSQL_SA_PASSWORD": password,
				},
				WaitingFor: wait.ForLog("SQL Server is now ready for client connections").
					WithStartupTimeout(3 * time.Minute),
			},
			Started: true,
		},
	)
	if err != nil {
		t.Skipf("SQL Server runtime parity unavailable: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	host, err := container.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := container.MappedPort(ctx, "1433/tcp")
	if err != nil {
		t.Fatal(err)
	}
	return (&url.URL{Scheme: "sqlserver", User: url.UserPassword("sa", password), Host: fmt.Sprintf("%s:%s", host, port.Port())}).String() + "?database=master&encrypt=false&trustservercertificate=true"
}

func testRemoteAgentRuntimeParity(t *testing.T, dsn string) {
	t.Helper()
	fixture := newRuntimeParityFixture(t, dsn)
	fixture.createPendingAgent(t, "agent-a")
	stateDir := t.TempDir()
	address := freeRuntimeAddress(t)
	bootstrap := exec.Command(runtimeAgentBinary(t), "")
	bootstrap.Env = append(
		os.Environ(),
		"DURPDEPLOY_AGENT_STATE_DIR="+stateDir,
		"DURPDEPLOY_AGENT_LISTEN_ADDR="+address,
		"DURPDEPLOY_AGENT_VERSION=runtime",
	)
	stdout, err := bootstrap.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := bootstrap.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if bootstrap.ProcessState == nil {
			_ = bootstrap.Process.Kill()
			_ = bootstrap.Wait()
		}
	})
	endpoint := "https://" + address
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() {
		t.Fatalf("read pairing code: %v", scanner.Err())
	}
	code, err := agentproto.ParsePairingCode(
		strings.TrimPrefix(scanner.Text(), "Pairing code: "),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !scanner.Scan() {
		t.Fatalf("read agent fingerprint: %v", scanner.Err())
	}
	agentPin, err := agentproto.ParseSHA256Pin(
		strings.TrimPrefix(scanner.Text(), "Agent fingerprint: "),
	)
	if err != nil {
		t.Fatal(err)
	}
	pull, err := agentproto.ParsePullEndpoint(fixture.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	serverPin, err := agentproto.ParseSHA256Pin(
		fixture.serverIdentity.Fingerprint.String(),
	)
	if err != nil {
		t.Fatal(err)
	}
	input := agentpairing.PairInput{
		Endpoint: endpoint,
		AgentPin: agentPin,
		Identity: fixture.serverIdentity,
		Request: agentproto.PairRequest{
			ProtocolEnvelope: agentproto.ProtocolEnvelope{
				Protocol: agentproto.AgentV1,
			},
			PairingCode:  code,
			AgentPin:     agentPin,
			ServerPin:    serverPin,
			PullEndpoint: pull,
			AgentID:      "agent-a",
		},
	}
	identity, err := agentpairing.Pair(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	codeHash := code.Hash()
	if _, err := fixture.repo.DB.ExecContext(
		context.Background(),
		"INSERT INTO agent_pairings (agent_id, pairing_code_hash, agent_public_identity, agent_pin, state, expires_at) VALUES (?, ?, ?, ?, 'pending', ?)",
		"agent-a",
		codeHash[:],
		identity.PublicIdentity,
		identity.AgentPin.String(),
		fixture.now.Add(time.Hour).Unix(),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repo.DB.ExecContext(
		context.Background(),
		"UPDATE agents SET status = 'active', certificate_pem = ?, certificate_fingerprint = ?, enrolled_at = ?, last_heartbeat_at = ? WHERE id = ?",
		identity.PublicIdentity,
		identity.AgentPin.String(),
		fixture.now.Unix(),
		fixture.now.Unix(),
		"agent-a",
	); err != nil {
		t.Fatal(err)
	}
	pairedIdentity, err := agenttls.LoadOrCreate(stateDir, "https://"+address)
	if err != nil {
		t.Fatal(err)
	}
	if err := bootstrap.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := bootstrap.Wait(); err == nil {
		t.Fatal("agent exited successfully after termination")
	}
	fixture.agentIdentity = pairedIdentity
	if _, err := fixture.repo.DB.ExecContext(
		context.Background(),
		"UPDATE agent_pairings SET state = 'paired', server_public_identity = ?, server_pin = ?, paired_at = ? WHERE agent_id = ?",
		"server",
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		fixture.now.Unix(),
		"agent-a",
	); err != nil {
		t.Fatal(err)
	}
	deploymentID := fixture.createWaitingDeployment(t, "runtime-payload")
	response := fixture.poll(
		t,
		fixture.agentIdentity,
		`{"protocol":"agent/1","agent_version":"runtime"}`,
	)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("poll status = %d", response.StatusCode)
	}
	var claim agentproto.PollResponse
	if err := json.NewDecoder(response.Body).Decode(&claim); err != nil {
		t.Fatal(err)
	}
	if response := fixture.lifecycle(
		t,
		fixture.agentIdentity,
		int64(claim.DeploymentID),
		"start",
		claimBody(string(claim.ClaimToken)),
	); response.StatusCode != http.StatusNoContent {
		t.Fatalf("start status = %d", response.StatusCode)
	}
	if response := fixture.lifecycle(
		t,
		fixture.agentIdentity,
		int64(claim.DeploymentID),
		"result",
		`{"protocol":"agent/1","claim_token":"`+string(
			claim.ClaimToken,
		)+`","state":"succeeded"}`,
	); response.StatusCode != http.StatusNoContent {
		t.Fatalf("result status = %d", response.StatusCode)
	}
	assertDispatchState(t, fixture, deploymentID, "succeeded")
	cancelID, cancelToken := runtimeClaim(t, fixture)
	if response := fixture.lifecycle(
		t,
		fixture.agentIdentity,
		cancelID,
		"start",
		claimBody(cancelToken),
	); response.StatusCode != http.StatusNoContent {
		t.Fatalf("cancel start = %d", response.StatusCode)
	}
	if _, err := fixture.repo.Queries.RequestDeploymentDispatchCancellation(
		context.Background(),
		db.RequestDeploymentDispatchCancellationParams{
			CancelRequestedAt: sql.NullInt64{
				Int64: fixture.now.Unix(),
				Valid: true,
			},
			DeploymentID: cancelID,
			CurrentState: "started",
		},
	); err != nil {
		t.Fatal(err)
	}
	if response := fixture.lifecycle(
		t,
		fixture.agentIdentity,
		cancelID,
		"cancelled",
		claimBody(cancelToken),
	); response.StatusCode != http.StatusNoContent {
		t.Fatalf("cancel ack = %d", response.StatusCode)
	}
	assertDispatchState(t, fixture, cancelID, "cancelled")
	lostID, lostToken := runtimeClaim(t, fixture)
	if response := fixture.lifecycle(
		t,
		fixture.agentIdentity,
		lostID,
		"start",
		claimBody(lostToken),
	); response.StatusCode != http.StatusNoContent {
		t.Fatalf("lost start = %d", response.StatusCode)
	}
	fixture.now = fixture.now.Add(agentproto.LostThreshold)
	if err := fixture.listener.Maintain(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertDispatchState(t, fixture, lostID, "lost")

	fixture.revoke(t, "agent-a")
	if response := fixture.poll(
		t,
		fixture.agentIdentity,
		`{"protocol":"agent/1","agent_version":"runtime"}`,
	); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked poll status = %d", response.StatusCode)
	}
}

func runtimeClaim(t *testing.T, fixture *pollFixture) (int64, string) {
	t.Helper()
	id := fixture.createWaitingDeployment(t, "runtime-payload")
	response := fixture.poll(
		t,
		fixture.agentIdentity,
		`{"protocol":"agent/1","agent_version":"runtime"}`,
	)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("poll status = %d", response.StatusCode)
	}
	var claim agentproto.PollResponse
	if err := json.NewDecoder(response.Body).Decode(&claim); err != nil {
		t.Fatal(err)
	}
	return id, string(claim.ClaimToken)
}

func newRuntimeParityFixture(t *testing.T, dsn string) *pollFixture {
	t.Helper()
	conn, err := migrate.Run(dsn)
	for attempt := 0; err != nil && attempt < 14; attempt++ {
		time.Sleep(2 * time.Second)
		conn, err = migrate.Run(dsn)
	}
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	fixture := &agentServerFixture{
		now:            time.Now().UTC().Truncate(time.Second),
		repo:           repository.New(conn),
		serverIdentity: loadTestIdentity(t),
		agentIdentity:  loadTestIdentity(t),
		box:            box,
	}
	listener, err := New(
		Config{
			Repository: fixture.repo,
			Identity:   fixture.serverIdentity,
			Now:        func() time.Time { return fixture.now },
			Box:        box,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	listener.pollWait = func(context.Context) error { return nil }
	server := httptest.NewUnstartedServer(listener.Handler())
	server.TLS = listener.TLSConfig()
	server.StartTLS()
	t.Cleanup(server.Close)
	fixture.listener, fixture.server = listener, server
	project, err := fixture.repo.Queries.CreateProject(
		context.Background(),
		db.CreateProjectParams{Name: "runtime-project"},
	)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := fixture.repo.Queries.CreateEnvironment(
		context.Background(),
		db.CreateEnvironmentParams{Name: "runtime-environment"},
	)
	if err != nil {
		t.Fatal(err)
	}
	release, err := fixture.repo.Queries.CreateRelease(
		context.Background(),
		db.CreateReleaseParams{
			ProjectID: project.ID,
			Version:   "runtime-v1",
			StepsJson: "[]",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return &pollFixture{
		agentServerFixture: fixture,
		releaseID:          release.ID,
		envID:              environment.ID,
	}
}

func freeRuntimeAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func runtimeAgentBinary(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "durpdeploy-agent")
	command := exec.Command(
		"go",
		"build",
		"-o",
		binary,
		"github.com/DeveloperDurp/durpdeploy-agent/cmd/agent",
	)
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build agent: %v: %s", err, output)
	}
	return binary
}
