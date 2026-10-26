package dispatch

import (
	"testing"

	"durpdeploy/internal/config"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/testutil"
	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
	"github.com/stretchr/testify/require"
)

func TestPollResourcePolicyChecksBeforeClaimLookup(t *testing.T) {
	conn, err := migrate.Run(":memory:?_pragma=foreign_keys(1)")
	require.NoError(t, err)
	t.Cleanup(func() { testutil.Close(t, conn) })
	settings := config.Defaults()
	settings.Limits.Execution.MaxRetries++
	d := New(repository.NewConfigured(conn, settings))
	// A closed database proves rejection precedes any claim query.
	require.NoError(t, conn.Close())
	response, claimed, err := d.Poll(t.Context(), "missing", PollRequest{})
	require.ErrorIs(t, err, resourcepolicy.ErrIncompatible)
	require.False(t, claimed)
	require.Empty(t, response.ClaimToken)
	require.Empty(t, response.Payload)
}
