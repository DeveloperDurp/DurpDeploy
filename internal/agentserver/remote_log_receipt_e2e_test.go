package agentserver_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/dispatch"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/secret"
	"durpdeploy/internal/server"
	"durpdeploy/internal/staging"

	agentpayload "github.com/DeveloperDurp/durpdeploy-agent/payload"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
	"github.com/robfig/cron/v3"
)

type receiptAcknowledgment struct {
	Status  int
	Body    string
	Headers http.Header
}

func TestRemoteLogAcknowledgmentsIndependentOfHiddenSecretsAPIE2E(
	t *testing.T,
) {
	for _, legacy := range []bool{false, true} {
		for _, limit := range []string{"count", "bytes", "gap"} {
			t.Run(
				fmt.Sprintf("legacy=%t/%s", legacy, limit),
				func(t *testing.T) {
					matched := receiptAcknowledgments(
						t,
						legacy,
						limit,
						"hidden-end",
					)
					unmatched := receiptAcknowledgments(
						t,
						legacy,
						limit,
						"unmatched-end",
					)
					if !reflect.DeepEqual(matched, unmatched) {
						t.Fatal(
							"acknowledgments depend on the excluded synthetic secret",
						)
					}
				},
			)
		}
	}
}

func receiptAcknowledgments(
	t *testing.T,
	legacy bool,
	limit, literal string,
) []receiptAcknowledgment {
	t.Helper()
	f, advertisement := newReceiptAgentFixture(t)
	api, id := provisionReceiptDeployment(t, f, legacy, literal)
	poll := pollReceiptClaim(t, f, id, advertisement, literal)
	path := func(pattern string) string { return strings.ReplaceAll(pattern, "{id}", fmt.Sprint(id)) }
	claim := fmt.Sprintf(
		`{"protocol":"agent/1","claim_token":%q}`,
		poll.ClaimToken,
	)
	assertAgentStatus(
		t,
		postAgent(t, f, path(agentproto.StartPath), claim),
		204,
	)
	var acknowledgments []receiptAcknowledgment
	post := func(body string, want int) {
		t.Helper()
		acknowledgments = append(
			acknowledgments,
			recordReceiptAcknowledgment(
				t,
				f,
				path(agentproto.LogsPath),
				body,
				want,
			),
		)
	}
	batch := func(sequence int, line string) string {
		t.Helper()
		return receiptLogBatch(t, claim, sequence, line)
	}
	// Include NUL in the compared input. Publication and encrypted retention
	// must both succeed even on databases whose text columns reject NUL.
	first := "\x00hidden-"
	if limit == "bytes" {
		first = strings.Repeat(
			"x",
			agentproto.MaxLogLineBytes-len(first),
		) + first
	}
	post(
		strings.Replace(
			batch(1, first),
			string(poll.ClaimToken),
			"wrong-claim",
			1,
		),
		409,
	)
	post(batch(1, first), 204)
	table := "remote_step_runs"
	if legacy {
		table = "remote_deployment_claims"
	}
	query := "SELECT log_buffer_ciphertext FROM " + table + " WHERE deployment_id=?"
	assertReceiptRollback(t, f, id, table, query, post, batch)
	count := agentproto.MaxLogEvents + 1
	if limit == "bytes" {
		count = agentproto.MaxLogBatchBytes/agentproto.MaxLogLineBytes + 1
	}
	if limit == "gap" {
		count = agentproto.MaxLogEvents
	}
	for sequence := 2; sequence <= count; sequence++ {
		line := ""
		if limit == "bytes" {
			line = strings.Repeat("x", agentproto.MaxLogLineBytes)
		}
		if limit == "gap" || limit == "bytes" {
			// Receipt 1 must make this exact forward-window edge independent
			// of whether its redaction tail remains unpublished.
			post(batch(sequence+1, line), 204)
		} else {
			post(batch(sequence, line), 204)
		}
	}
	f = restartAgentFixture(t, f)
	f.client.Timeout = 30 * time.Second
	post(batch(1, "changed duplicate"), 204)
	if limit == "gap" || limit == "bytes" {
		post(batch(count+2, "end safe text\n"), 409)
		post(batch(2, ""), 204)
	}
	post(batch(count+2, "end safe text\n"), 204)
	finishReceiptClaim(t, f, id, claim, query, post,
		batch(count+3, "terminal write"))
	logs := string(
		api("GET", fmt.Sprintf("/deployments/%d/logs.txt", id), nil, 200),
	)
	if strings.Contains(logs, literal) {
		t.Fatal("hidden secret reached public logs")
	}
	return acknowledgments
}

func newReceiptAgentFixture(t *testing.T) (agentFixture, string) {
	t.Helper()
	f := newAgentFixtureWithDSN(t, filepath.Join(t.TempDir(), "receipt.db")+
		"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", nil)
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	f.repo.SetSecretBox(box)
	var advertisementRequest agentproto.PollRequest
	if err := json.Unmarshal([]byte(pollBody), &advertisementRequest); err != nil {
		t.Fatal(err)
	}
	recordFixturePoll(t, f, advertisementRequest)
	if err := f.repo.Queries.AddAgentStagingCapability(t.Context(), "test-agent"); err != nil {
		t.Fatal(err)
	}
	advertisement, err := f.repo.ResourceLimits().AgentPolicy().Advertisement()
	if err != nil {
		t.Fatal(err)
	}
	if err := f.repo.Queries.AddAgentResourcePolicy(t.Context(), db.AddAgentResourcePolicyParams{
		AgentID: "test-agent", Advertisement: advertisement,
	}); err != nil {
		t.Fatal(err)
	}
	return f, advertisement
}

func provisionReceiptDeployment(
	t *testing.T, f agentFixture, legacy bool, literal string,
) (func(string, string, any, int) []byte, int64) {
	t.Helper()
	api, stopCoordinator := receiptManagementAPI(t, f)
	f.client.Timeout = 30 * time.Second
	var project, environment, release, deployment struct{ ID int64 }
	decode := func(raw []byte, target any) {
		t.Helper()
		if err := json.Unmarshal(raw, target); err != nil {
			t.Fatal(err)
		}
	}
	decode(
		api("POST", "/projects", map[string]string{"name": "receipt"}, 201),
		&project,
	)
	decode(
		api("POST", "/environments", map[string]string{"name": "receipt"}, 201),
		&environment,
	)
	api(
		"POST",
		"/admin/agents/test-agent/labels",
		map[string]string{"label": "receipt"},
		204,
	)
	api(
		"POST",
		"/admin/agents/test-agent/environments",
		map[string]int64{"environment_id": environment.ID},
		204,
	)
	base := fmt.Sprintf("/projects/%d", project.ID)
	api(
		"POST",
		base+"/variables",
		map[string]any{"name": "SELECTED", "value": "harmless"},
		201,
	)
	api(
		"POST",
		base+"/variables",
		map[string]any{"name": "EXCLUDED", "value": literal, "secret": true},
		201,
	)
	api("POST", base+"/steps", map[string]any{
		"name": strings.Repeat(
			"a",
			150,
		) + " password=" + strings.Repeat(
			"b",
			95,
		),
		"interpreter": "bash", "script_body": "echo harmless",
		"execution_target": "agent", "agent_execution_mode": "host",
		"agent_selectors": []string{
			"receipt",
		}, "variable_names": []string{"SELECTED"},
	}, 201)
	decode(
		api("POST", base+"/releases", map[string]string{"version": "v1"}, 201),
		&release,
	)
	if legacy {
		stopCoordinator()
	}
	decode(api("POST", base+"/deployments", map[string]int64{
		"release_id": release.ID, "environment_id": environment.ID,
	}, 201), &deployment)
	id := deployment.ID
	// Modern APIs create step claims. Reconstruct an old server's durable claim
	// for the legacy ingestion path after provisioning public resources.
	if legacy {
		if _, err := f.repo.DB.ExecContext(t.Context(), "UPDATE deployments SET status='pending',assigned_agent_id='test-agent' WHERE id=?", id); err != nil {
			t.Fatal(err)
		}
		if _, err := f.repo.Queries.CreateRemoteDeploymentClaim(t.Context(), id); err != nil {
			t.Fatal(err)
		}
	}
	return api, id
}

func pollReceiptClaim(
	t *testing.T, f agentFixture, id int64, advertisement, literal string,
) agentproto.PollResponse {
	t.Helper()
	poll := decodePollResponse(t, postAgent(
		t,
		f,
		agentproto.PollPath,
		pollBody,
		http.Header{
			resourcepolicy.CapabilityHeader: {advertisement},
			staging.CapabilityHeader:        {staging.Capability},
		},
	))
	plaintext, err := agentpayload.Open(f.identity, id, []byte(poll.Payload))
	if err != nil {
		t.Fatal(err)
	}
	var payload dispatch.Payload
	if err := json.Unmarshal(plaintext, &payload); err != nil {
		t.Fatal(err)
	}
	selected := false
	for _, variable := range payload.Variables {
		if variable.Name == "EXCLUDED" || variable.Value == literal {
			t.Fatal("hidden secret reached dispatched payload")
		}
		selected = selected || variable.Name == "SELECTED"
	}
	if !selected {
		t.Fatal("harmless allowlist did not reach payload")
	}
	return poll
}

func recordReceiptAcknowledgment(
	t *testing.T, f agentFixture, path, body string, want int,
) receiptAcknowledgment {
	t.Helper()
	response := postAgent(t, f, path, body)
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != want {
		t.Fatalf(
			"logs status=%d want=%d error=%v",
			response.StatusCode,
			want,
			err,
		)
	}
	headers := response.Header.Clone()
	headers.Del("Date")
	return receiptAcknowledgment{response.StatusCode, string(data), headers}
}

func receiptLogBatch(
	t *testing.T,
	claim string,
	sequence int,
	line string,
) string {
	t.Helper()
	encoded, err := json.Marshal(line)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSuffix(
		claim,
		"}",
	) + fmt.Sprintf(
		`,"events":[{"sequence":%d,"line":%s}]}`,
		sequence,
		encoded,
	)
}

func assertReceiptRollback(
	t *testing.T, f agentFixture, id int64, table, query string,
	post func(string, int), batch func(int, string) string,
) {
	t.Helper()
	var beforeBuffer string
	if err := f.repo.DB.QueryRowContext(t.Context(), query, id).Scan(&beforeBuffer); err != nil {
		t.Fatal(err)
	}
	beforeLogs, err := f.repo.Queries.ListDeploymentLogsByDeployment(
		t.Context(),
		id,
	)
	if err != nil {
		t.Fatal(err)
	}
	stream := f.broker.Subscribe(id)
	defer f.broker.Unsubscribe(id, stream)
	if _, err := f.repo.DB.ExecContext(t.Context(), "CREATE TRIGGER fail_receipt BEFORE UPDATE OF log_buffer_ciphertext ON "+table+" BEGIN SELECT RAISE(FAIL,'receipt write failed'); END"); err != nil {
		t.Fatal(err)
	}
	post(batch(2, "end\n"), 500)
	if _, err := f.repo.DB.ExecContext(t.Context(), "DROP TRIGGER fail_receipt"); err != nil {
		t.Fatal(err)
	}
	var afterBuffer string
	if err := f.repo.DB.QueryRowContext(t.Context(), query, id).Scan(&afterBuffer); err != nil ||
		afterBuffer != beforeBuffer {
		t.Fatal("failed write advanced encrypted state")
	}
	afterLogs, err := f.repo.Queries.ListDeploymentLogsByDeployment(
		t.Context(),
		id,
	)
	if err != nil ||
		receiptProducerLogCount(
			afterLogs,
		) != receiptProducerLogCount(
			beforeLogs,
		) {
		t.Fatal("failed write committed log rows")
	}
	assertNoReceiptBroadcast(t, stream)
}

func receiptProducerLogCount(logs []db.DeploymentLog) int {
	count := 0
	for _, log := range logs {
		if strings.HasSuffix(log.Line, ` agent_id="test-agent"`) {
			count++
		}
	}
	return count
}

func assertNoReceiptBroadcast(t *testing.T, stream <-chan string) {
	t.Helper()
	// The coordinator can publish its independent "Step started" row while
	// this request runs. Only authenticated producer rows belong to this write.
	for {
		select {
		case line := <-stream:
			if strings.HasSuffix(line, ` agent_id="test-agent"`) {
				t.Fatal("failed write broadcast logs")
			}
		default:
			return
		}
	}
}

func finishReceiptClaim(
	t *testing.T, f agentFixture, id int64, claim, query string,
	post func(string, int), terminalWrite string,
) {
	t.Helper()
	resultPath := strings.ReplaceAll(
		agentproto.ResultPath,
		"{id}",
		fmt.Sprint(id),
	)
	result := strings.TrimSuffix(
		claim,
		"}",
	) + `,"state":"succeeded","error":""}`
	// This fixture uploads chosen logs instead of executing a script. Remove
	// its empty staging input so terminal success needs no fabricated artifact.
	if _, err := f.repo.DB.ExecContext(t.Context(), "DELETE FROM deployment_staging_chunks WHERE deployment_id=?", id); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.DB.ExecContext(t.Context(), "DELETE FROM deployment_staging_snapshots WHERE deployment_id=?", id); err != nil {
		t.Fatal(err)
	}
	assertAgentStatus(
		t,
		postAgent(t, f, resultPath, result),
		204,
	)
	assertAgentStatus(
		t,
		postAgent(t, f, resultPath, result),
		204,
	)
	post(terminalWrite, 409)
	var ciphertext *string
	if err := f.repo.DB.QueryRowContext(t.Context(), query, id).Scan(&ciphertext); err != nil ||
		ciphertext != nil {
		t.Fatalf("terminal state not cleared: %v", err)
	}
}

func receiptManagementAPI(
	t *testing.T,
	f agentFixture,
) (func(string, string, any, int) []byte, func()) {
	t.Helper()
	r := runner.New(f.repo, f.broker)
	t.Cleanup(r.KillAll)
	app := httptest.NewServer(server.NewRouter(
		f.repo,
		r,
		cron.NewParser(
			cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow,
		),
		handler.NewAuthHandler(f.repo),
	))
	t.Cleanup(app.Close)
	user, err := f.repo.Queries.CreateUser(t.Context(), db.CreateUserParams{
		Email: "receipt@test.local", Name: "Receipt", PasswordHash: "unused", Role: "admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	token, prefix, hash, err := auth.MintApiToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.Queries.CreateApiToken(t.Context(), db.CreateApiTokenParams{
		ID: "receipt", UserID: user.ID, Name: "receipt", TokenPrefix: prefix, TokenHash: hash, Scope: "global",
	}); err != nil {
		t.Fatal(err)
	}
	return func(method, path string, body any, want int) []byte {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		if body == nil {
			raw = nil
		}
		request, err := http.NewRequestWithContext(
			t.Context(),
			method,
			app.URL+"/api/v1"+path,
			bytes.NewReader(raw),
		)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != want {
			t.Fatalf(
				"%s %s status=%d want=%d body=%s error=%v",
				method,
				path,
				response.StatusCode,
				want,
				data,
				err,
			)
		}
		return data
	}, r.KillAll
}
