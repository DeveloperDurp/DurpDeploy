package agentserver_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/secret"
	"durpdeploy/internal/server"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	"github.com/robfig/cron/v3"
)

func TestSplitCredentialRedactionAPIWebE2E(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for _, credential := range []string{
			"Bearer synthetic-proof", "ghp_" + strings.Repeat("S", 36),
			"AKIAABCDEFGHIJKLMNOP", "xoxb-synthetic-proof",
			"xoxa-synthetic-proof", "xoxp-synthetic-proof",
		} {
			t.Run(fmt.Sprintf("legacy=%t/%s", legacy, credential[:4]),
				func(t *testing.T) {
					assertSplitCredentialIngress(t, legacy, credential)
				})
		}
	}
}

func assertSplitCredentialIngress(
	t *testing.T,
	legacy bool,
	credential string,
) {
	t.Helper()
	f := newAgentFixtureWithDSN(t, filepath.Join(t.TempDir(), "logs.db")+
		"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", nil)
	var id int64
	var claim string
	if legacy {
		seedAgentLifecycle(t, f.repo)
		id, claim = 1, `{"protocol":"agent/1","claim_token":"claim-one"}`
	} else {
		id, claim = claimedRemoteStep(t, f)
	}
	// The existing polling fixture contains a literal secret. Remove it so
	// this regression exercises only default patterns, then encrypt pending logs.
	if _, err := f.repo.DB.ExecContext(t.Context(), "DELETE FROM release_variables"); err != nil {
		t.Fatal(err)
	}
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	f.repo.SetSecretBox(box)
	path := func(pattern string) string {
		return strings.ReplaceAll(pattern, "{id}", fmt.Sprint(id))
	}
	if response := postAgent(t, f, path(agentproto.StartPath), claim); response.StatusCode != http.StatusNoContent {
		t.Fatalf("start status=%d", response.StatusCode)
	}
	before, err := f.repo.Queries.ListDeploymentLogsByDeployment(
		t.Context(),
		id,
	)
	if err != nil {
		t.Fatal(err)
	}
	stream := f.broker.Subscribe(id)
	t.Cleanup(func() { f.broker.Unsubscribe(id, stream) })
	read := newPrefixPublicReader(t, f, id)
	live := openPrefixStreams(t, read)
	text := "0" + credential + "\n"
	post := func(sequence int, line string, identity string, want int) {
		t.Helper()
		body := strings.TrimSuffix(identity, "}") + fmt.Sprintf(
			`,"events":[{"sequence":%d,"line":%q}]}`, sequence, line)
		if response := postAgent(t, f, path(agentproto.LogsPath), body); response.StatusCode != want {
			t.Fatalf("logs status=%d want=%d", response.StatusCode, want)
		}
	}
	// The producer is authenticated, but a different claim cannot mutate logs.
	var wrongClaim map[string]string
	if err := json.Unmarshal([]byte(claim), &wrongClaim); err != nil {
		t.Fatal(err)
	}
	wrongClaim["claim_token"] = "wrong-claim"
	wrong, err := json.Marshal(wrongClaim)
	if err != nil {
		t.Fatal(err)
	}
	post(1, text[:4], string(wrong), http.StatusConflict)
	post(1, text[:4], claim, http.StatusNoContent)
	logs, err := f.repo.Queries.ListDeploymentLogsByDeployment(t.Context(), id)
	if err != nil || len(logs) != len(before) {
		t.Fatalf("partial prefix persisted: rows=%d error=%v", len(logs), err)
	}
	select {
	case <-stream:
		t.Fatal("partial prefix broadcast before its continuation")
	default:
	}
	var encrypted string
	query := "SELECT log_buffer_ciphertext FROM remote_step_runs WHERE deployment_id=?"
	if legacy {
		query = "SELECT log_buffer_ciphertext FROM remote_deployment_claims WHERE deployment_id=?"
	}
	if err := f.repo.DB.QueryRowContext(t.Context(), query, id).
		Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	plaintext, err := box.Decrypt(encrypted)
	if err != nil {
		t.Fatalf("pending buffer not encrypted: error=%v", err)
	}
	var pending struct {
		Events []struct {
			Sequence int64
			Line     string
		}
	}
	if err := json.Unmarshal([]byte(plaintext), &pending); err != nil ||
		len(pending.Events) != 1 || pending.Events[0].Sequence != 1 ||
		pending.Events[0].Line != text[:4] {
		t.Fatalf("encrypted buffer lost the pending event: error=%v", err)
	}
	// Retry after a listener restart, then continue and replay the whole batch.
	f = restartAgentFixture(t, f)
	post(1, text[:4], claim, http.StatusNoContent)
	post(2, text[4:], claim, http.StatusNoContent)
	post(1, text[:4], claim, http.StatusNoContent)
	post(2, text[4:], claim, http.StatusNoContent)
	logs, err = f.repo.Queries.ListDeploymentLogsByDeployment(t.Context(), id)
	if err != nil || len(logs) != len(before)+2 {
		t.Fatalf("persisted log count=%d error=%v", len(logs), err)
	}
	assertPrefixPersistedLogs(t, logs, len(before), legacy, stream)
	// Finish the claim through HTTP. Mark the fixture's remaining steps complete
	// so public replay streams terminate without running unrelated scripts.
	result := strings.TrimSuffix(
		claim,
		"}",
	) + `,"state":"succeeded","error":""}`
	if response := postAgent(t, f, path(agentproto.ResultPath), result); response.StatusCode != http.StatusNoContent {
		t.Fatalf("result status=%d", response.StatusCode)
	}
	if err := f.repo.Queries.UpdateDeploymentStatus(t.Context(),
		db.UpdateDeploymentStatusParams{ID: id, Status: "succeeded"}); err != nil {
		t.Fatal(err)
	}
	assertPrefixStreams(t, live, credential)
	assertPrefixPublicSurfaces(t, read, credential)
}

func assertPrefixPersistedLogs(
	t *testing.T, logs []db.DeploymentLog, before int, legacy bool,
	stream <-chan string,
) {
	t.Helper()
	// The legacy list query groups by scope; inspect persistence in ID order.
	sort.Slice(logs, func(i, j int) bool { return logs[i].ID < logs[j].ID })
	var joined string
	for index, log := range logs[before:] {
		if !strings.HasSuffix(log.Line, ` agent_id="test-agent"`) ||
			(index > 0 && log.ID <= logs[before+index-1].ID) ||
			(!legacy && (!log.StepIndex.Valid || log.StepIndex.Int64 != 0)) {
			t.Fatal("log ordering or attribution changed")
		}
		joined += strings.TrimSuffix(log.Line, ` agent_id="test-agent"`)
		select {
		case broadcast := <-stream:
			if broadcast != log.Line {
				t.Fatal("broadcast differs from persisted log")
			}
		default:
			t.Fatal("persisted log was not broadcast")
		}
	}
	if joined != `0[REDACTED]\n` {
		t.Fatalf("stored credential escaped: %q", joined)
	}
	select {
	case <-stream:
		t.Fatal("replay duplicated a broadcast")
	default:
	}
}

type prefixStreamResult struct {
	data []byte
	err  error
}

func openPrefixStreams(
	t *testing.T, read func(string, bool) *http.Response,
) map[string]chan prefixStreamResult {
	t.Helper()
	live := make(map[string]chan prefixStreamResult)
	for _, format := range []string{"sse", "structured", "ndjson"} {
		response := read(
			"/api/v1/deployments/%d/logs/stream?format="+format,
			true,
		)
		if response.StatusCode != http.StatusOK {
			response.Body.Close()
			t.Fatalf("live %s status=%d", format, response.StatusCode)
		}
		channel := make(chan prefixStreamResult, 1)
		live[format] = channel
		t.Cleanup(func() { response.Body.Close() })
		go func() {
			data, err := io.ReadAll(response.Body)
			channel <- prefixStreamResult{data, err}
		}()
	}
	return live
}

func assertPrefixStreams(
	t *testing.T, live map[string]chan prefixStreamResult, credential string,
) {
	t.Helper()
	for format, channel := range live {
		select {
		case result := <-channel:
			if result.err != nil {
				t.Fatalf("live %s: %v", format, result.err)
			}
			assertPrefixResponse(t, "live "+format, result.data, credential)
		case <-time.After(10 * time.Second):
			t.Fatalf("live %s did not complete", format)
		}
	}
}

func newPrefixPublicReader(
	t *testing.T, f agentFixture, id int64,
) func(string, bool) *http.Response {
	t.Helper()
	r := runner.New(f.repo, f.broker)
	t.Cleanup(r.KillAll)
	app := httptest.NewServer(server.NewRouter(f.repo, r,
		cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
		handler.NewAuthHandler(f.repo)))
	t.Cleanup(app.Close)
	user, err := f.repo.Queries.CreateUser(t.Context(), db.CreateUserParams{
		Email: "prefix@test.local", Name: "Prefix reader",
		PasswordHash: "unused", Role: "admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	token, prefix, hash, err := auth.MintApiToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.Queries.CreateApiToken(t.Context(), db.CreateApiTokenParams{
		ID: "prefix", UserID: user.ID, Name: "prefix", TokenPrefix: prefix,
		TokenHash: hash, Scope: "global",
	}); err != nil {
		t.Fatal(err)
	}
	session, csrf, err := auth.NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.Queries.CreateSession(t.Context(), db.CreateSessionParams{
		ID: session, UserID: user.ID, CsrfToken: csrf,
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	return func(surface string, authenticated bool) *http.Response {
		t.Helper()
		request, err := http.NewRequestWithContext(t.Context(), "GET",
			app.URL+fmt.Sprintf(surface, id), nil)
		if err != nil {
			t.Fatal(err)
		}
		if authenticated {
			if strings.HasPrefix(surface, "/api/") {
				request.Header.Set("Authorization", "Bearer "+token)
			} else {
				request.AddCookie(&http.Cookie{Name: "session", Value: session})
			}
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
}

func assertPrefixPublicSurfaces(
	t *testing.T, read func(string, bool) *http.Response, credential string,
) {
	t.Helper()
	for _, surface := range []string{
		"/api/v1/deployments/%d/logs", "/api/v1/deployments/%d/logs/page",
		"/api/v1/deployments/%d/logs.txt",
		"/api/v1/deployments/%d/logs/stream",
		"/api/v1/deployments/%d/logs/stream?format=structured",
		"/api/v1/deployments/%d/logs/stream?format=ndjson",
		"/deployments/%d", "/deployments/%d/logs.txt",
		"/deployments/%d/logs/stream?format=structured&preview=true",
	} {
		response := read(surface, true)
		data, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr != nil || response.StatusCode != 200 {
			t.Fatalf(
				"%s status=%d error=%v",
				surface,
				response.StatusCode,
				readErr,
			)
		}
		assertPrefixResponse(t, surface, data, credential)
	}
	for _, surface := range []string{
		"/api/v1/deployments/%d/logs", "/api/v1/deployments/%d/logs/stream",
	} {
		response := read(surface, false)
		response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf(
				"unauthenticated %s status=%d",
				surface,
				response.StatusCode,
			)
		}
	}
}

func assertPrefixResponse(
	t *testing.T,
	surface string,
	data []byte,
	credential string,
) {
	t.Helper()
	if strings.Contains(string(data), credential[5:]) ||
		!strings.Contains(string(data), "0[REDACTED]") ||
		!strings.Contains(string(data), "test-agent") {
		t.Fatalf("%s lost redaction or attribution", surface)
	}
}
