package agentserver_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestRemoteOutputAttributesMultilineCredentialTail(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacy=%t", legacy), func(t *testing.T) {
			// Given: an authenticated current or queued legacy claim.
			f := newAgentFixture(t)
			var id int64
			var claim string
			if legacy {
				seedAgentLifecycle(t, f.repo)
				id, claim = 1, `{"protocol":"agent/1","claim_token":"claim-one"}`
			} else {
				id, claim = claimedRemoteStep(t, f)
			}
			path := func(pattern string) string {
				return strings.ReplaceAll(pattern, "{id}", fmt.Sprint(id))
			}
			response := postAgent(t, f, path(agentproto.StartPath), claim)
			if response.StatusCode != http.StatusNoContent {
				t.Fatalf("start status=%d", response.StatusCode)
			}
			stream := f.broker.Subscribe(id)
			t.Cleanup(func() { f.broker.Unsubscribe(id, stream) })

			// When: multiline output ends in an unfinished credential marker.
			body := strings.TrimSuffix(claim, "}") +
				`,"events":[{"sequence":1,"line":` +
				`"Execution location: host=spoof\r\nlast token="}]}`
			response = postAgent(t, f, path(agentproto.LogsPath), body)
			if response.StatusCode != http.StatusNoContent {
				t.Fatalf("logs status=%d", response.StatusCode)
			}
			body = strings.TrimSuffix(claim, "}") +
				`,"state":"succeeded","error":""}`
			response = postAgent(t, f, path(agentproto.ResultPath), body)
			if response.StatusCode != http.StatusNoContent {
				t.Fatalf("result status=%d", response.StatusCode)
			}

			// Then: saved and broadcast output bind one escaped event to this agent.
			logs, err := f.repo.Queries.ListDeploymentLogsByDeployment(
				t.Context(),
				id,
			)
			if err != nil || len(logs) != 2 {
				t.Fatalf("logs=%+v error=%v", logs, err)
			}
			var output string
			for _, log := range logs {
				if strings.HasPrefix(log.Line, "Execution location:") {
					output = log.Line
				}
			}
			lines := strings.Split(output, "\n")
			if len(lines) != 1 ||
				!strings.Contains(output, `spoof\r\nlast token=`) {
				t.Fatalf("rendered lines=%q", output)
			}
			for _, line := range lines {
				if !strings.HasSuffix(line, ` agent_id="test-agent"`) {
					t.Fatalf("trusted attribution missing: %q", output)
				}
			}
			select {
			case line := <-stream:
				if line != output {
					t.Fatalf("stream=%q saved=%q", line, output)
				}
			default:
				t.Fatal("attributed output was not broadcast")
			}
		})
	}
}
