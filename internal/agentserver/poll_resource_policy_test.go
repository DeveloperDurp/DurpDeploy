package agentserver_test

import (
	"database/sql"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/config"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
	"github.com/stretchr/testify/require"
)

func TestPollResourcePolicyRejectsBeforeMutation(t *testing.T) {
	advertisement, err := resourcepolicy.Defaults().Advertisement()
	require.NoError(t, err)
	for name, values := range map[string][]string{
		"empty":       {""},
		"repeated":    {advertisement, advertisement},
		"empty-first": {"", advertisement},
		"empty-last":  {advertisement, ""},
		"joined":      {advertisement + ", " + advertisement},
		"unknown":     {"resource-policy/2;sha256=" + strings.Repeat("a", 64)},
		"short":       {"resource-policy/1;sha256=" + strings.Repeat("a", 63)},
		"invalid-hex": {"resource-policy/1;sha256=" + strings.Repeat("g", 64)},
		"uppercase":   {"resource-policy/1;sha256=" + strings.Repeat("A", 64)},
		"malformed":   {"untrusted-policy-do-not-echo"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newAgentFixture(t)
			id := seedPollPayload(t, f, "pending", "test-agent")
			before, err := f.repo.Queries.GetAgent(t.Context(), "test-agent")
			require.NoError(t, err)
			claim, err := f.repo.Queries.GetRemoteDeploymentClaim(
				t.Context(),
				id,
			)
			require.NoError(t, err)
			capabilities, err := f.repo.Queries.ListAgentPollCapabilities(
				t.Context(),
				"test-agent",
			)
			require.NoError(t, err)
			response := pollWithResourcePolicy(t, f, values)
			defer response.Body.Close()
			require.Equal(t, http.StatusConflict, response.StatusCode)
			require.Empty(
				t,
				response.Header.Values(resourcepolicy.CapabilityHeader),
			)
			body, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.Empty(t, body)
			after, err := f.repo.Queries.GetAgent(t.Context(), "test-agent")
			require.NoError(t, err)
			require.Equal(t, before, after)
			afterClaim, err := f.repo.Queries.GetRemoteDeploymentClaim(
				t.Context(),
				id,
			)
			require.NoError(t, err)
			require.Equal(t, claim, afterClaim)
			afterCapabilities, err := f.repo.Queries.ListAgentPollCapabilities(
				t.Context(),
				"test-agent",
			)
			require.NoError(t, err)
			require.Equal(t, capabilities, afterCapabilities)
		})
	}
}

func TestPollResourcePolicyAcknowledgesClaim(t *testing.T) {
	limits := resourcepolicy.Defaults()
	limits.Container.TmpfsBytes = 512 << 20
	advertisement, err := limits.Advertisement()
	require.NoError(t, err)
	for _, legacy := range []bool{false, true} {
		t.Run(
			map[bool]string{false: "advertised", true: "legacy"}[legacy],
			func(t *testing.T) {
				f := newAgentFixture(t)
				id := seedPollPayload(t, f, "pending", "test-agent")
				var headers []string
				if !legacy {
					headers = []string{advertisement}
				}
				response := pollWithResourcePolicy(t, f, headers)
				require.Equal(t, http.StatusOK, response.StatusCode)
				require.Equal(
					t,
					headers,
					response.Header.Values(resourcepolicy.CapabilityHeader),
				)
				poll := decodePollResponse(t, response)
				require.Equal(t, id, int64(poll.DeploymentID))
				require.NotEmpty(t, poll.ClaimToken)
				require.NotEmpty(t, poll.Payload)
				_, err := f.repo.Queries.GetAgentResourcePolicy(
					t.Context(), "test-agent",
				)
				require.ErrorIs(t, err, sql.ErrNoRows)
			},
		)
	}
}

func TestPollResourcePolicyCustomIdle(t *testing.T) {
	settings := config.Defaults()
	settings.Limits.Execution.MaxRetries++
	f := newAgentFixtureWithDSN(
		t,
		":memory:?_pragma=foreign_keys(1)",
		nil,
		settings,
	)
	defaults, err := resourcepolicy.Defaults().Advertisement()
	require.NoError(t, err)
	f.client.Timeout = 27 * time.Second
	response := pollWithResourcePolicy(t, f, []string{defaults})
	defer response.Body.Close()
	require.Equal(t, http.StatusNoContent, response.StatusCode)
	require.Equal(
		t,
		[]string{defaults},
		response.Header.Values(resourcepolicy.CapabilityHeader),
	)
}

func pollWithResourcePolicy(
	t *testing.T,
	f agentFixture,
	values []string,
) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		f.server.URL+agentproto.PollPath, strings.NewReader(pollBody))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	for _, value := range values {
		request.Header.Add(resourcepolicy.CapabilityHeader, value)
	}
	response, err := f.client.Do(request)
	require.NoError(t, err)
	return response
}
