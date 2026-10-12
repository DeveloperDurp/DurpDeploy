//go:build e2e && agentstaging

package agentserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"durpdeploy/internal/db"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestLegacyExecutionLocationAPIWebE2E(t *testing.T) {
	// Given: an already queued legacy claim after a server upgrade.
	s := newStagingE2EConfigured(t, stagingFixtureConfig{})
	seedAgentLifecycle(t, s.f.repo)
	startLifecycle(t, s.f, http.StatusNoContent)
	startLifecycle(t, s.f, http.StatusNoContent)

	// When: its authenticated agent uploads output and completes the claim.
	response := postAgent(t, s.f,
		strings.ReplaceAll(agentproto.LogsPath, "{id}", "1"),
		`{"protocol":"agent/1","claim_token":"claim-one",`+
			`"events":[{"sequence":1,"line":"legacy-output agent_id=\"spoof\"\nunfinished token="}]}`,
	)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("legacy log status=%d", response.StatusCode)
	}
	response = postAgent(t, s.f,
		strings.ReplaceAll(agentproto.ResultPath, "{id}", "1"),
		`{"protocol":"agent/1","claim_token":"claim-one",`+
			`"state":"succeeded","error":""}`,
	)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("legacy result status=%d", response.StatusCode)
	}

	// Then: public logs and saved web logs retain server-derived attribution.
	var logs []db.DeploymentLog
	if err := json.Unmarshal(s.api(t, "GET", "/deployments/1/logs", nil, 200), &logs); err != nil {
		t.Fatal(err)
	}
	if len(logs) != 2 || !strings.HasPrefix(logs[0].Line, "Execution agent:") ||
		!strings.Contains(logs[0].Line, `agent="test" id="test-agent"`) ||
		!strings.HasSuffix(logs[1].Line, ` agent_id="test-agent"`) {
		t.Fatalf("legacy public logs lost attribution: %+v", logs)
	}
	for _, line := range strings.Split(logs[1].Line, "\n") {
		if !strings.HasSuffix(line, ` agent_id="test-agent"`) {
			t.Fatalf("legacy public line lost attribution: %q", line)
		}
	}
	for _, surface := range []string{
		"/logs.txt", "/logs/stream?format=structured", "/logs/stream?format=ndjson",
	} {
		body := string(s.api(t, "GET", "/deployments/1"+surface, nil, 200))
		if !strings.Contains(body, "Execution agent:") ||
			!strings.Contains(body, "legacy-output") ||
			!strings.Contains(body, "test-agent") {
			t.Fatalf("legacy attribution missing from %s: %s", surface, body)
		}
		t.Logf("Legacy public log surface: %s status=200", surface)
	}
	s.assertExecutionLocationWeb(t, 1, 0, 1)
}
