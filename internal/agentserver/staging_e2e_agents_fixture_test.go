//go:build e2e && agentstaging

package agentserver_test

import (
	"encoding/pem"
	"testing"
	"time"

	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
)

func (s *stagingE2E) startStagingAgents(t *testing.T, binary string) {
	t.Helper()
	s.startAgent(t, binary, "test-agent", s.f.identityDir)
	directory := t.TempDir()
	identity, err := agenttls.LoadOrCreate(directory, "https://second-agent")
	if err != nil {
		t.Fatal(err)
	}
	cert := string(
		pem.EncodeToMemory(
			&pem.Block{
				Type:  "CERTIFICATE",
				Bytes: identity.Certificate.Certificate[0],
			},
		),
	)
	if _, err := s.f.repo.DB.ExecContext(
		t.Context(),
		`INSERT INTO agents(id,name,endpoint,status,certificate_pem,certificate_fingerprint,encrypted_identity) VALUES('second-agent','second','https://second-agent','active',?,?,'fixture')`,
		cert,
		identity.Fingerprint.String(),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := s.f.repo.DB.ExecContext(
		t.Context(),
		`INSERT INTO agent_pairings(agent_id,pairing_code_hash,agent_public_identity,agent_pin,state,expires_at,paired_at,server_public_identity,server_pin,encrypted_identity) VALUES('second-agent',randomblob(32),?,?,'paired',1,1,'fixture',?,'fixture')`,
		cert,
		identity.Fingerprint.String(),
		s.f.serverIdentity.Fingerprint.String(),
	); err != nil {
		t.Fatal(err)
	}
	s.startAgent(t, binary, "second-agent", directory)
	for _, id := range []string{"test-agent", "second-agent"} {
		deadline := time.Now().Add(time.Minute)
		for {
			var count int
			if err := s.f.repo.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM agent_staging_capabilities WHERE agent_id=?", id).
				Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count == 1 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s did not advertise staging", id)
			}
			time.Sleep(100 * time.Millisecond)
		}
		s.api(
			t,
			"POST",
			"/admin/agents/"+id+"/labels",
			map[string]string{"label": id},
			204,
		)
	}
}
