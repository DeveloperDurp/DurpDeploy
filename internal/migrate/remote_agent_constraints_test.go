package migrate

import (
	"database/sql"
	"testing"
)

func assertRemoteConstraints(t *testing.T, conn *sql.DB) {
	t.Helper()
	for _, query := range []string{
		`INSERT INTO agents(id,name,endpoint) VALUES('a','a','https://a')`,
		`INSERT INTO agents(id,name,endpoint) VALUES('b','b','https://b')`,
		`INSERT INTO environments(id,name) VALUES(2,'second')`,
		`INSERT INTO agent_environment_labels(agent_id,environment_id)
		 VALUES('a',1),('a',2)`,
		`INSERT INTO agent_labels(agent_id,label)
		 VALUES('a','web'),('a','linux'),('b','web')`,
		`INSERT INTO step_agent_selectors(step_id,label)
		 VALUES(1,'web'),(1,'linux')`,
		`INSERT INTO deployment_steps
		 (deployment_id,step_index,name,script_body)
		 VALUES(1,0,'frozen','echo frozen'),(1,1,'next','echo next')`,
		`INSERT INTO deployment_step_selectors
		 (deployment_id,step_index,label) VALUES(1,0,'web'),(1,0,'linux')`,
		`INSERT INTO deployment_step_attempts
		 (deployment_id,step_index,attempt,wait_deadline)
		 VALUES(1,0,1,300),(1,1,1,300)`,
		`INSERT INTO deployment_dispatches
		 (deployment_id,step_index,attempt,ciphertext)
		 VALUES(1,0,1,'encrypted'),(1,1,1,'encrypted')`,
		`INSERT INTO deployment_logs(id,deployment_id,line)
		 VALUES(9,1,'step zero'),(10,1,'step one'),(11,1,'duplicate')`,
		`INSERT INTO deployment_log_scopes
		 (log_id,deployment_id,step_index,attempt,sequence)
		 VALUES(9,1,0,1,1),(10,1,1,1,1)`,
	} {
		_, err := conn.Exec(query)
		requireNoError(t, err, query)
	}
	for _, query := range []string{
		`INSERT INTO agent_environment_labels(agent_id,environment_id)
		 VALUES('a',1)`,
		`INSERT INTO agent_environment_labels(agent_id,environment_id)
		 VALUES('a',999)`,
		`INSERT INTO agent_labels(agent_id,label) VALUES('a','web')`,
		`INSERT INTO agent_labels(agent_id,label) VALUES('a','')`,
		`INSERT INTO step_agent_selectors(step_id,label) VALUES(1,'web')`,
		`INSERT INTO deployment_step_selectors
		 (deployment_id,step_index,label) VALUES(1,0,'web')`,
		`INSERT INTO deployment_steps
		 (deployment_id,step_index,name,script_body)
		 VALUES(1,0,'duplicate','echo bad')`,
		`INSERT INTO deployment_step_attempts
		 (deployment_id,step_index,attempt,wait_deadline)
		 VALUES(1,0,2,300)`,
		`INSERT INTO deployment_dispatches
		 (deployment_id,step_index,attempt) VALUES(1,0,2)`,
		`INSERT INTO deployment_log_scopes
		 (log_id,deployment_id,step_index,attempt,sequence)
		 VALUES(11,1,0,1,1)`,
		`INSERT INTO deployment_log_scopes
		 (log_id,deployment_id,step_index,attempt,sequence)
		 VALUES(11,1,0,2,1)`,
		`INSERT INTO deployment_log_scopes
		 (log_id,deployment_id,step_index,attempt,sequence)
		 VALUES(11,2,0,1,1)`,
		`INSERT INTO deployment_log_scopes
		 (log_id,deployment_id,step_index,sequence) VALUES(11,1,0,1)`,
		`UPDATE steps SET execution_target='remote' WHERE id=1`,
		`UPDATE agents SET status='active' WHERE id='a'`,
		`UPDATE deployment_step_attempts SET state='claimed'`,
		`UPDATE deployment_step_attempts SET state='bogus'`,
		`UPDATE deployment_step_attempts SET state='succeeded'`,
		`DELETE FROM deployments WHERE id=1`,
		`DELETE FROM deployment_logs WHERE id=7`,
	} {
		if _, err := conn.Exec(query); err == nil {
			t.Fatalf("expected rejection: %s", query)
		} else {
			t.Logf("rejected: %s: %v", query, err)
		}
	}
	t.Log(
		"PASS: environment labels, multi-valued selectors, " +
			"attempt and log scope constraints",
	)
}
