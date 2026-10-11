package agentserver_test

import "testing"

func TestAgentFleetRevocationResetsStagingCapabilityE2E(t *testing.T) {
	// Given an agent whose previous binary advertised staging support.
	f := newAgentFixture(t)
	srv := fleetAdminServer(t, f)
	if err := f.repo.Queries.AddAgentStagingCapability(
		t.Context(), "test-agent",
	); err != nil {
		t.Fatal(err)
	}

	// When the owner revokes the identity before a downgrade and re-pairing.
	fleetRequest(t, srv, "POST", "/api/v1/admin/agents/test-agent/revoke",
		"admin", "", 204)

	// Then the next identity cannot inherit stale staging eligibility.
	var capabilities int
	if err := f.repo.DB.QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM agent_staging_capabilities WHERE agent_id=?",
		"test-agent",
	).Scan(&capabilities); err != nil || capabilities != 0 {
		t.Fatalf("staging capabilities=%d error=%v", capabilities, err)
	}
}
