package agentserver_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/dispatch"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
	"github.com/stretchr/testify/require"
)

func TestRemoteStepResultCompletesRun(t *testing.T) {
	fixture := newAgentFixture(t)
	deploymentID, claim := claimedRemoteStep(t, fixture)
	path := func(pattern string) string {
		return strings.ReplaceAll(pattern, "{id}", fmt.Sprint(deploymentID))
	}
	response := postAgent(t, fixture, path(agentproto.StartPath), claim)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("start status=%d", response.StatusCode)
	}
	result := strings.Replace(
		claim,
		"}",
		`,"state":"succeeded","error":""}`,
		1,
	)
	response = postAgent(t, fixture, path(agentproto.ResultPath), result)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("result status=%d", response.StatusCode)
	}
	runs, err := fixture.repo.Queries.ListRemoteStepRuns(
		t.Context(),
		db.ListRemoteStepRunsParams{DeploymentID: deploymentID, StepIndex: 0},
	)
	if err != nil || len(runs) != 1 || runs[0].State != "succeeded" ||
		!runs[0].FinishedAt.Valid {
		t.Fatalf("remote runs=%+v error=%v", runs, err)
	}
}

func TestRemoteStepLogsRedactSplitSecret(t *testing.T) {
	fixture := newAgentFixture(t)
	deploymentID, claim := claimedRemoteStep(t, fixture)
	deployment, err := fixture.repo.Queries.GetDeployment(
		t.Context(),
		deploymentID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repo.DB.Exec(`INSERT INTO release_variables
		(release_id,name,value,secret) VALUES (?,'TOKEN','step-secret',1)`,
		deployment.ReleaseID); err != nil {
		t.Fatal(err)
	}
	path := func(pattern string) string {
		return strings.ReplaceAll(pattern, "{id}", fmt.Sprint(deploymentID))
	}
	response := postAgent(t, fixture, path(agentproto.StartPath), claim)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("start status=%d", response.StatusCode)
	}
	stream := fixture.broker.Subscribe(deploymentID)
	t.Cleanup(func() { fixture.broker.Unsubscribe(deploymentID, stream) })
	for sequence, line := range []string{"step-", "secret"} {
		body := strings.Replace(
			claim,
			"}",
			fmt.Sprintf(
				`,"events":[{"sequence":%d,"line":%q}]}`,
				sequence+1,
				line,
			),
			1,
		)
		response = postAgent(t, fixture, path(agentproto.LogsPath), body)
		if response.StatusCode != http.StatusNoContent {
			t.Fatalf("log status=%d", response.StatusCode)
		}
	}
	result := strings.Replace(
		claim,
		"}",
		`,"state":"succeeded","error":""}`,
		1,
	)
	response = postAgent(t, fixture, path(agentproto.ResultPath), result)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("result status=%d", response.StatusCode)
	}
	logs, err := fixture.repo.Queries.ListDeploymentLogsByDeployment(
		t.Context(), deploymentID,
	)
	identity := ` agent_id="test-agent"`
	if err != nil || len(logs) != 3 ||
		strings.TrimSuffix(logs[0].Line, identity)+
			strings.TrimSuffix(logs[1].Line, identity) != "[REDACTED]" ||
		!strings.HasSuffix(logs[0].Line, identity) ||
		!strings.HasSuffix(logs[1].Line, identity) ||
		!logs[0].StepName.Valid ||
		!logs[0].StepIndex.Valid ||
		logs[0].StepIndex.Int64 != 0 ||
		logs[0].StepState.Valid {
		t.Fatalf("stored step logs=%+v error=%v", logs, err)
	}
	var streamed strings.Builder
	for range 2 {
		select {
		case line := <-stream:
			if !strings.HasSuffix(line, identity) {
				t.Fatalf("streamed line lost authenticated identity: %q", line)
			}
			streamed.WriteString(strings.TrimSuffix(line, identity))
		default:
			t.Fatal("stored step log was not broadcast")
		}
	}
	if got := streamed.String(); got != "[REDACTED]" {
		t.Fatalf("streamed step logs=%q", got)
	}
}

func TestRemoteStepStartRejectsExpiredClaim(t *testing.T) {
	fixture := newAgentFixture(t)
	deploymentID, claim := claimedRemoteStep(t, fixture)
	if _, err := fixture.repo.DB.ExecContext(
		t.Context(),
		`UPDATE remote_step_runs SET claim_expires_at = unixepoch()
		 WHERE deployment_id = ? AND step_index = 0`,
		deploymentID,
	); err != nil {
		t.Fatal(err)
	}
	path := strings.ReplaceAll(
		agentproto.StartPath,
		"{id}",
		fmt.Sprint(deploymentID),
	)
	response := postAgent(t, fixture, path, claim)
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("expired start status=%d", response.StatusCode)
	}
}

func TestRemoteStepCancellationAcknowledgement(t *testing.T) {
	for _, scenario := range []string{
		"requested", "expired", "late", "before-publication", "recovery",
		"recovery-sibling",
	} {
		t.Run(scenario, func(t *testing.T) {
			checkPrestartTransferCancellation(t, scenario)
		})
	}
}

func checkPrestartTransferCancellation(t *testing.T, scenario string) {
	t.Helper()
	fixture := newAgentFixture(t)
	deploymentID, claim := claimedRemoteStep(t, fixture)
	if err := fixture.repo.Queries.MarkPrestartRemoteStepInput(
		t.Context(), db.MarkPrestartRemoteStepInputParams{
			DeploymentID: deploymentID, StepIndex: 0, AgentID: "test-agent",
		},
	); err != nil {
		t.Fatal(err)
	}
	requestPrestartTransferCancellation(t, fixture, deploymentID, scenario)
	runs, err := fixture.repo.Queries.ListRemoteStepRuns(
		t.Context(),
		db.ListRemoteStepRunsParams{DeploymentID: deploymentID, StepIndex: 0},
	)
	if err != nil || len(runs) != 1 || runs[0].State != "cancel_requested" {
		t.Fatalf(
			"pre-start transfer did not preserve cleanup barrier: %v, %v",
			runs,
			err,
		)
	}
	start := strings.ReplaceAll(
		agentproto.StartPath, "{id}", fmt.Sprint(deploymentID),
	)
	assertAgentStatus(
		t,
		postAgent(t, fixture, start, claim),
		http.StatusConflict,
	)
	deferred := scenario != "requested" && scenario != "expired"
	if deferred {
		next := seedPollPayload(t, fixture, "pending", "test-agent")
		checkLegacyClaimBlocked(t, fixture, next)
		checkPrestartCleanupBarrier(t, fixture, deploymentID, scenario)
		checkLegacyClaimBlocked(t, fixture, next)
	}
	path := strings.ReplaceAll(
		agentproto.CancelledPath,
		"{id}",
		fmt.Sprint(deploymentID),
	)
	for range 2 {
		assertAgentStatus(
			t, postAgent(t, fixture, path, claim), http.StatusNoContent,
		)
	}
	runs, err = fixture.repo.Queries.ListRemoteStepRuns(
		t.Context(),
		db.ListRemoteStepRunsParams{DeploymentID: deploymentID, StepIndex: 0},
	)
	if err != nil || len(runs) != 1 || !runs[0].FinishedAt.Valid {
		t.Fatalf("remote runs=%+v error=%v", runs, err)
	}
	if !deferred {
		if runs[0].State != "cancelled" {
			t.Fatalf("acknowledged state=%s", runs[0].State)
		}
		return
	}
	if runs[0].State != "cleanup_unconfirmed" ||
		!runs[0].CleanupConfirmedAt.Valid {
		t.Fatalf(
			"late cleanup acknowledgement changed run history: %+v",
			runs[0],
		)
	}
	if scenario == "before-publication" {
		publishPrestartCleanupStatus(t, fixture, deploymentID)
	}
	blockers, err := fixture.repo.Queries.ListDeploymentQueueBlockers(
		t.Context(), deploymentID,
	)
	if err != nil || len(blockers) != 0 {
		t.Fatalf("confirmed cleanup left queue blockers: %v, %v", blockers, err)
	}
	ready, err := fixture.repo.Queries.ListWaitingRemoteDeploymentClaims(
		t.Context(), "test-agent",
	)
	require.NoError(t, err)
	require.Len(t, ready, 1)
}

func requestPrestartTransferCancellation(
	t *testing.T, fixture agentFixture, deploymentID int64, scenario string,
) {
	t.Helper()
	ctx := t.Context()
	switch {
	case strings.HasPrefix(scenario, "recovery"):
		_, err := fixture.repo.Queries.CancelOrphanedRemoteStepRuns(ctx, 1)
		require.NoError(t, err)
	case scenario == "requested":
		_, err := fixture.repo.Queries.RequestRemoteStepCancellation(
			ctx, db.RequestRemoteStepCancellationParams{
				Now:          validInt(1),
				DeploymentID: deploymentID,
			},
		)
		require.NoError(t, err)
	default:
		_, err := fixture.repo.DB.ExecContext(
			ctx,
			"UPDATE remote_step_runs SET claim_expires_at=1 WHERE deployment_id=?",
			deploymentID,
		)
		require.NoError(t, err)
		require.NoError(t, dispatch.New(fixture.repo).Maintain(ctx))
	}
}

func checkLegacyClaimBlocked(t *testing.T, fixture agentFixture, id int64) {
	t.Helper()
	ctx := t.Context()
	ready, err := fixture.repo.Queries.ListWaitingRemoteDeploymentClaims(
		ctx, "test-agent",
	)
	require.NoError(t, err)
	require.Empty(t, ready)
	now, err := fixture.repo.Queries.CurrentUnixTime(ctx)
	require.NoError(t, err)
	claimed, err := fixture.repo.ClaimRemoteDeployment(
		ctx, db.ClaimRemoteDeploymentParams{
			DeploymentID: id, AgentID: "test-agent",
			ClaimTokenHash: make([]byte, 32),
			Ciphertext: validString(
				"test",
			), Now: now, ClaimExpiresAt: now + 60,
		},
	)
	require.NoError(t, err)
	require.Zero(t, claimed)
}

func checkPrestartCleanupBarrier(
	t *testing.T, fixture agentFixture, deploymentID int64, scenario string,
) {
	t.Helper()
	ctx := t.Context()
	_, err := fixture.repo.DB.ExecContext(
		ctx,
		"UPDATE remote_step_runs SET cancel_requested_at=1 WHERE deployment_id=?",
		deploymentID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if scenario == "recovery-sibling" {
		// The earlier terminal run was not marked by startup recovery.
		_, err := fixture.repo.DB.ExecContext(ctx, `
UPDATE remote_step_runs SET recovery_cancelled=0 WHERE deployment_id=?;
INSERT INTO remote_step_runs(deployment_id,step_index,agent_id,state,recovery_cancelled)
VALUES(?,1,'test-agent','cancelled',1);`,
			deploymentID, deploymentID)
		require.NoError(t, err)
	}
	if err := dispatch.New(fixture.repo).Maintain(ctx); err != nil {
		t.Fatal(err)
	}
	runs, err := fixture.repo.Queries.ListRemoteStepRuns(
		ctx,
		db.ListRemoteStepRunsParams{DeploymentID: deploymentID, StepIndex: 0},
	)
	if err != nil || len(runs) != 1 || runs[0].State != "cleanup_unconfirmed" {
		t.Fatalf(
			"pre-start transfer lost its cleanup barrier: %v, %v",
			runs,
			err,
		)
	}
	deployment, err := fixture.repo.Queries.GetDeployment(ctx, deploymentID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(scenario, "recovery") {
		if deployment.Status != "cleanup_unconfirmed" {
			t.Fatalf("orphaned deployment status=%s", deployment.Status)
		}
	} else if deployment.Status != "running" {
		t.Fatalf("maintenance published active deployment status %s", deployment.Status)
	}
	if scenario == "late" {
		publishPrestartCleanupStatus(t, fixture, deploymentID)
	}
}

func publishPrestartCleanupStatus(
	t *testing.T,
	fixture agentFixture,
	id int64,
) {
	t.Helper()
	if err := fixture.repo.Queries.UpdateDeploymentStatus(
		t.Context(), db.UpdateDeploymentStatusParams{
			ID: id, Status: "cleanup_unconfirmed",
		},
	); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteStepPrestartCleanupBlocksNativeClaim(t *testing.T) {
	fixture := newAgentFixture(t)
	ctx := t.Context()
	id, claim := claimedRemoteStepWithMode(
		t,
		fixture,
		"container",
		containerPollBody,
	)
	require.NoError(t, fixture.repo.Queries.MarkPrestartRemoteStepInput(
		ctx, db.MarkPrestartRemoteStepInputParams{
			DeploymentID: id, StepIndex: 0, AgentID: "test-agent",
		},
	))
	now, err := fixture.repo.Queries.CurrentUnixTime(ctx)
	require.NoError(t, err)
	_, err = fixture.repo.Queries.RequestRemoteStepCancellation(
		ctx, db.RequestRemoteStepCancellationParams{
			DeploymentID: id, Now: validInt(now),
		},
	)
	require.NoError(t, err)
	next := seedWaitingNativeContainerStep(t, fixture)
	ready, err := fixture.repo.Queries.ListWaitingRemoteStepRuns(
		ctx,
		"test-agent",
	)
	require.NoError(t, err)
	require.Empty(t, ready)
	claimed, err := fixture.repo.Queries.ClaimRemoteStepRun(
		ctx, db.ClaimRemoteStepRunParams{
			DeploymentID: next, StepIndex: 0, AgentID: "test-agent",
			ClaimTokenHash: make([]byte, 32), Ciphertext: validString("test"),
			ClaimExpiresAt: validInt(now + 60), Now: validInt(now),
		},
	)
	require.NoError(t, err)
	require.Zero(t, claimed)
	path := strings.ReplaceAll(agentproto.CancelledPath, "{id}", fmt.Sprint(id))
	assertAgentStatus(
		t,
		postAgent(t, fixture, path, claim),
		http.StatusNoContent,
	)
	ready, err = fixture.repo.Queries.ListWaitingRemoteStepRuns(
		ctx,
		"test-agent",
	)
	require.NoError(t, err)
	require.Len(t, ready, 1)
	require.Equal(t, next, ready[0].DeploymentID)
}

func seedWaitingNativeContainerStep(t *testing.T, fixture agentFixture) int64 {
	t.Helper()
	ctx := t.Context()
	id := seedPollPayload(t, fixture, "pending", "test-agent")
	deployment, err := fixture.repo.Queries.GetDeployment(ctx, id)
	require.NoError(t, err)
	_, err = fixture.repo.DB.ExecContext(ctx, `
DELETE FROM remote_deployment_claims WHERE deployment_id=?;
UPDATE deployments SET status='running', assigned_agent_id=NULL WHERE id=?;
UPDATE deployment_steps SET agent_execution_mode='container',
  container_image='harbor.durp.info/dockerhub/library/alpine:3.20' WHERE deployment_id=?;
INSERT INTO agent_environment_labels(agent_id,environment_id) VALUES('test-agent',?);
INSERT INTO deployment_step_selectors(deployment_id,step_index,label) VALUES(?,0,'test');`,
		id, id, id, deployment.EnvironmentID, id)
	require.NoError(t, err)
	created, err := fixture.repo.QueueRemoteStepRuns(ctx, id, 0)
	require.NoError(t, err)
	require.EqualValues(t, 1, created)
	return id
}

func claimedRemoteStep(
	t *testing.T,
	fixture agentFixture,
) (int64, string) {
	t.Helper()
	return claimedRemoteStepWithMode(t, fixture, "host", pollBody)
}

func claimedRemoteStepWithMode(
	t *testing.T,
	fixture agentFixture,
	mode, body string,
) (int64, string) {
	t.Helper()
	deploymentID := seedPollPayload(t, fixture, "pending", "test-agent")
	deployment, err := fixture.repo.Queries.GetDeployment(
		t.Context(),
		deploymentID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repo.DB.ExecContext(t.Context(), `
		DELETE FROM remote_deployment_claims WHERE deployment_id = ?;
		UPDATE deployments SET status = 'running', assigned_agent_id = NULL
		WHERE id = ?;
		INSERT INTO agent_labels(agent_id,label) VALUES('test-agent','test');
		INSERT INTO agent_environment_labels(agent_id,environment_id)
		VALUES('test-agent',?);
		INSERT INTO deployment_step_selectors(deployment_id,step_index,label)
		VALUES(?,0,'test')`, deploymentID, deploymentID,
		deployment.EnvironmentID, deploymentID,
	); err != nil {
		t.Fatal(err)
	}
	if mode == "container" {
		if _, err := fixture.repo.DB.ExecContext(t.Context(), `
        UPDATE agents SET agent_protocol = 'agent/3' WHERE id = 'test-agent';
        INSERT INTO agent_execution_modes VALUES('test-agent','container');
        INSERT INTO agent_container_runtimes VALUES('test-agent','podman');
        INSERT INTO agent_container_interpreters VALUES('test-agent','bash');
        UPDATE deployment_steps SET agent_execution_mode = 'container',
            container_image = 'harbor.durp.info/dockerhub/library/alpine:3.20', variable_names = '["MODE"]'
            WHERE deployment_id = ?;`, deploymentID); err != nil {
			t.Fatal(err)
		}
	}
	advertisement, err := fixture.repo.ResourceLimits().
		AgentPolicy().
		Advertisement()
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.repo.Queries.AddAgentResourcePolicy(t.Context(), db.AddAgentResourcePolicyParams{
		AgentID: "test-agent", Advertisement: advertisement,
	}); err != nil {
		t.Fatal(err)
	}
	created, err := fixture.repo.QueueRemoteStepRuns(
		t.Context(), deploymentID, 0,
	)
	if err != nil || created != 1 {
		t.Fatalf("queue remote step rows=%d error=%v", created, err)
	}
	poll := decodePollResponse(
		t,
		postAgent(t, fixture, agentproto.PollPath, body, http.Header{
			resourcepolicy.CapabilityHeader: {advertisement},
		}),
	)
	claim := fmt.Sprintf(
		`{"protocol":"agent/1","claim_token":%q}`,
		poll.ClaimToken,
	)
	if mode == "container" {
		claim = strings.Replace(claim, "agent/1", "agent/3", 1)
	}
	return deploymentID, claim
}
