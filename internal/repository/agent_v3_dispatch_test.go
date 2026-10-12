package repository_test

import (
	"fmt"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func TestAgentV3MixedFleetAndClaimRecheck(t *testing.T) {
	for _, test := range []struct {
		mode, interpreter string
		want              int64
	}{
		{"host", "bash", 3}, {"host", "python3", 1}, {"container", "bash", 1}, {"container", "pwsh", 1},
	} {
		t.Run(test.mode+"/"+test.interpreter, func(t *testing.T) {
			r := remoteFixture(t)
			_, err := r.DB.ExecContext(
				t.Context(),
				`
                INSERT INTO agents(id,name,endpoint,status,certificate_pem,certificate_fingerprint,encrypted_identity)
                    SELECT 'c','c',endpoint,status,certificate_pem,lower(hex(zeroblob(32))),encrypted_identity FROM agents WHERE id='b';
                INSERT INTO agent_pairings(agent_id,pairing_code_hash,agent_public_identity,agent_pin,
                    server_public_identity,server_pin,encrypted_identity,state,expires_at,paired_at)
                    SELECT 'c',zeroblob(32),agent_public_identity,lower(hex(zeroblob(32))),server_public_identity,server_pin,encrypted_identity,state,expires_at,paired_at
                    FROM agent_pairings WHERE agent_id='b';
                INSERT INTO agent_interpreters VALUES('c','bash');
                UPDATE agents SET agent_protocol='agent/1' WHERE id='a';
                UPDATE agents SET agent_protocol='agent/2' WHERE id='b';
                UPDATE agents SET agent_protocol='agent/3' WHERE id='c';
                INSERT INTO agent_interpreters VALUES('a','python3');
                INSERT INTO agent_interpreters VALUES('b','python3');
                INSERT INTO agent_execution_modes VALUES('c','host'),('c','container');
                INSERT INTO agent_container_runtimes VALUES('c','podman');
                INSERT INTO agent_container_interpreters VALUES('c','bash'),('c','pwsh');
                INSERT INTO agent_labels VALUES('a','other'),('b','other'),('c','other'),('c','linux');
                INSERT INTO agent_environment_labels(agent_id,environment_id) VALUES('a',2),('b',2),('c',2);
                UPDATE deployments SET status='running' WHERE id=3;
                UPDATE deployment_steps SET agent_execution_mode=?1, interpreter=?2,
                    container_image=CASE WHEN ?1='container' THEN 'alpine:3.20' ELSE '' END
                    WHERE deployment_id=3 AND step_index=0;`,
				test.mode,
				test.interpreter,
			)
			if err != nil {
				t.Fatal(err)
			}
			created, err := r.QueueRemoteStepRuns(t.Context(), 3, 0)
			if err != nil || created != test.want {
				t.Fatalf("created=%d want=%d error=%v", created, test.want, err)
			}
			runs, err := r.Queries.ListRemoteStepRuns(
				t.Context(),
				db.ListRemoteStepRunsParams{DeploymentID: 3, StepIndex: 0},
			)
			if err != nil {
				t.Fatal(err)
			}
			if test.mode == "container" && runs[0].AgentID != "c" {
				t.Fatalf("container targets=%+v", runs)
			}
			// Eligibility must be checked again when labels or readiness change after fan-out.
			for _, mutation := range []string{
				"DELETE FROM agent_labels WHERE label='other'",
				"DELETE FROM agent_environment_labels",
				"DELETE FROM agent_container_runtimes",
			} {
				if mutation == "DELETE FROM agent_container_runtimes" &&
					test.mode != "container" {
					continue
				}
				if _, err := r.DB.ExecContext(t.Context(), mutation); err != nil {
					t.Fatal(err)
				}
				for _, run := range runs {
					_, claimed, err := r.ClaimRemoteStepPayload(
						t.Context(),
						run.AgentID,
						func(repository.RemotePayloadSnapshot) (repository.RemotePreparedClaim, error) {
							t.Fatal("ineligible work reached encryption")
							return repository.RemotePreparedClaim{}, nil
						},
					)
					if err != nil || claimed {
						t.Fatalf(
							"claim=%v error=%v after %s",
							claimed,
							err,
							mutation,
						)
					}
				}
				var restore string
				switch mutation {
				case "DELETE FROM agent_labels WHERE label='other'":
					restore = "INSERT INTO agent_labels VALUES('a','other'),('b','other'),('c','other')"
				case "DELETE FROM agent_environment_labels":
					restore = "INSERT INTO agent_environment_labels(agent_id,environment_id) VALUES('a',2),('b',2),('c',2)"
				case "DELETE FROM agent_container_runtimes":
					restore = "INSERT INTO agent_container_runtimes VALUES('c','podman')"
				}
				if _, err := r.DB.ExecContext(t.Context(), restore); err != nil {
					t.Fatal(err)
				}
			}
			t.Log(
				fmt.Sprintf(
					"%s %s routed to %d compatible agents",
					test.mode,
					test.interpreter,
					created,
				),
			)
		})
	}
}
