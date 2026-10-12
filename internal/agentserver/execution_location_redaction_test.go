package agentserver_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"durpdeploy/internal/logscrub"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestRemoteStepLocationKeepsIdentityWhenMarkerIsSecret(t *testing.T) {
	for _, secret := range []string{
		"Execution", "Execution location: target=agent",
		`Execution location: target=agent host="physical-host" mode=host/shell attempt=1`,
	} {
		for _, alreadyRedacted := range []bool{false, true} {
			t.Run(
				fmt.Sprintf("%s/agent-redacted=%t", secret, alreadyRedacted),
				func(t *testing.T) {
					// Given: a secret overlaps the location marker.
					fixture := newAgentFixture(t)
					id, claim := claimedRemoteStep(t, fixture)
					deployment, err := fixture.repo.Queries.GetDeployment(
						t.Context(),
						id,
					)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := fixture.repo.DB.ExecContext(t.Context(), `
					INSERT INTO release_variables (release_id,name,value,secret)
					VALUES (?,'TOKEN',?,1)`, deployment.ReleaseID, secret,
					); err != nil {
						t.Fatal(err)
					}
					path := func(pattern string) string {
						return strings.ReplaceAll(
							pattern,
							"{id}",
							fmt.Sprint(id),
						)
					}
					response := postAgent(
						t,
						fixture,
						path(agentproto.StartPath),
						claim,
					)
					if response.StatusCode != http.StatusNoContent {
						t.Fatalf("start status=%d", response.StatusCode)
					}
					line := `Execution location: target=agent host="physical-host" mode=host/shell attempt=1`
					if alreadyRedacted {
						line = logscrub.New([]string{secret}).Scrub(line)
					}

					// When: the authenticated agent uploads its location.
					body := strings.Replace(claim, "}", fmt.Sprintf(
						`,"events":[{"sequence":1,"line":%q}]}`, line,
					), 1)
					response = postAgent(
						t,
						fixture,
						path(agentproto.LogsPath),
						body,
					)
					if response.StatusCode != http.StatusNoContent {
						t.Fatalf("log status=%d", response.StatusCode)
					}

					// Then: redaction retains authenticated attribution.
					logs, err := fixture.repo.Queries.ListDeploymentLogsByDeployment(
						t.Context(),
						id,
					)
					if err != nil || len(logs) != 2 {
						t.Fatalf("logs=%+v error=%v", logs, err)
					}
					if strings.Contains(logs[0].Line, secret) ||
						!strings.Contains(
							logs[0].Line,
							`agent_id="test-agent"`,
						) {
						t.Fatalf(
							"location lost safe attribution: %q",
							logs[0].Line,
						)
					}
				},
			)
		}
	}
}
