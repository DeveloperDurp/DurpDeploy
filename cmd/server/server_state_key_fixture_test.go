package main

import (
	"database/sql"
	"durpdeploy/internal/testutil"
	"encoding/json"
	"path/filepath"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/secret"
)

type serverStateKeyFixture struct {
	repo     *repository.Repository
	rotation secretKeyRotation
}

func newServerStateKeyFixture(t *testing.T) serverStateKeyFixture {
	t.Helper()
	return newServerStateKeyFixtureDSN(
		t,
		filepath.Join(t.TempDir(), "rotation.db"),
	)
}

func newServerStateKeyFixtureDSN(
	t *testing.T,
	dsn string,
) serverStateKeyFixture {
	t.Helper()
	conn, err := migrate.Run(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	})
	oldBox, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	key[0] = 1
	newBox, err := secret.NewBox(key)
	if err != nil {
		t.Fatal(err)
	}
	f := serverStateKeyFixture{
		repository.New(conn),
		secretKeyRotation{oldBox: oldBox, newBox: newBox},
	}
	statements := []string{
		"INSERT INTO users(email,password_hash,name,role) VALUES('rotation@example.test','unused','rotation','admin')",
		"INSERT INTO projects(name) VALUES('server-state')",
		"INSERT INTO environments(name) VALUES('server-state')",
		"INSERT INTO releases(project_id,version,steps_json) VALUES(1,'1','[]')",
		"INSERT INTO deployments(release_id,environment_id,status) VALUES(1,1,'pending')",
		"INSERT INTO deployment_steps(deployment_id,step_index,name,script_body) VALUES(1,0,'rotation','echo test')",
		"INSERT INTO agents(id,name,endpoint,created_at,updated_at) VALUES('agent','rotation','https://agent.test',100,200)",
	}
	for _, statement := range statements {
		if _, err := conn.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f serverStateKeyFixture) rotate(t *testing.T, plaintext bool) error {
	t.Helper()
	rotation := f.rotation
	rotation.plaintext = plaintext
	return f.repo.WithTx(t.Context(), func(q *db.Queries) error {
		return rotateStoredCredentials(t.Context(), q, rotation)
	})
}

type serverStateStore struct {
	name, insert, read, metadata string
	enrollment                   bool
}

var serverStateStores = []serverStateStore{
	{
		name:     "totp",
		insert:   "INSERT INTO mfa_totp(user_id,encrypted_seed,last_accepted_step,created_at,updated_at) VALUES(1,?,123,100,200)",
		read:     "SELECT encrypted_seed FROM mfa_totp",
		metadata: "SELECT user_id,last_accepted_step,created_at,updated_at FROM mfa_totp",
	},
	{
		name:       "enrollment",
		enrollment: true,
		insert:     "INSERT INTO mfa_challenges(token_hash,user_id,purpose,csrf_hash,ceremony_json,attempts,expires_at,created_at) VALUES(zeroblob(32),1,'totp_enroll',zeroblob(32),?,3,123456,100)",
		read:       "SELECT ceremony_json FROM mfa_challenges",
		metadata:   "SELECT token_hash,user_id,session_id,purpose,csrf_hash,attempts,expires_at,created_at FROM mfa_challenges",
	},
	{
		name:     "project-template",
		insert:   "INSERT INTO project_templates(source_project_id,created_by,name,description,snapshot_json,created_at) VALUES(1,1,'rotation','template',?,100)",
		read:     "SELECT snapshot_json FROM project_templates",
		metadata: "SELECT id,source_project_id,created_by,name,description,created_at FROM project_templates",
	},
	{
		name:     "agent",
		insert:   "UPDATE agents SET encrypted_identity = ?",
		read:     "SELECT encrypted_identity FROM agents",
		metadata: "SELECT id,status,certificate_pem,certificate_fingerprint,last_heartbeat_at,revoked_at,created_at,updated_at FROM agents",
	},
	{
		name:     "pairing",
		insert:   "INSERT INTO agent_pairings(agent_id,pairing_code_hash,agent_public_identity,agent_pin,encrypted_identity,expires_at,created_at,updated_at) VALUES('agent',zeroblob(32),'identity',printf('%064d',1),?,123456,100,200)",
		read:     "SELECT encrypted_identity FROM agent_pairings",
		metadata: "SELECT agent_id,pairing_code_hash,agent_public_identity,agent_pin,server_public_identity,server_pin,state,expires_at,paired_at,created_at,updated_at,server_pull_endpoint FROM agent_pairings",
	},
	{
		name:     "claim",
		insert:   "INSERT INTO remote_deployment_claims(deployment_id,agent_id,state,claim_token_hash,ciphertext,claim_expires_at,last_heartbeat_at,log_buffer_ciphertext,created_at,updated_at) VALUES(1,'agent','claimed',zeroblob(32),'agent-sealed-payload',123456,123,?,100,200)",
		read:     "SELECT log_buffer_ciphertext FROM remote_deployment_claims",
		metadata: "SELECT deployment_id,agent_id,state,reason,claim_token_hash,ciphertext,claim_expires_at,last_heartbeat_at,started_at,finished_at,cancel_requested_at,cleanup_confirmed_at,created_at,updated_at FROM remote_deployment_claims",
	},
	{
		name:     "step",
		insert:   "INSERT INTO remote_step_runs(deployment_id,step_index,agent_id,state,claim_token_hash,ciphertext,claim_expires_at,last_heartbeat_at,log_buffer_ciphertext,created_at,updated_at) VALUES(1,0,'agent','claimed',zeroblob(32),'agent-sealed-payload',123456,123,?,100,200)",
		read:     "SELECT log_buffer_ciphertext FROM remote_step_runs",
		metadata: "SELECT deployment_id,step_index,agent_id,state,claim_token_hash,ciphertext,claim_expires_at,last_heartbeat_at,started_at,finished_at,cancel_requested_at,cleanup_confirmed_at,recovery_cancelled,created_at,updated_at FROM remote_step_runs",
	},
}

func (s serverStateStore) seed(
	t *testing.T,
	f serverStateKeyFixture,
	value string,
) {
	t.Helper()
	if s.enrollment {
		encoded, err := json.Marshal(
			map[string]string{"encrypted_seed": value, "extra": "retained"},
		)
		if err != nil {
			t.Fatal(err)
		}
		value = string(encoded)
	}
	if _, err := f.repo.DB.ExecContext(
		t.Context(),
		s.insert,
		value,
	); err != nil {
		t.Fatal(err)
	}
}

func (s serverStateStore) ciphertext(
	t *testing.T,
	f serverStateKeyFixture,
) string {
	t.Helper()
	var value string
	if err := f.repo.DB.QueryRowContext(t.Context(), s.read).
		Scan(&value); err != nil {
		t.Fatal(err)
	}
	if s.enrollment {
		var ceremony map[string]string
		if err := json.Unmarshal([]byte(value), &ceremony); err != nil {
			t.Fatal(err)
		}
		if ceremony["extra"] != "retained" {
			t.Fatal("ceremony metadata changed")
		}
		value = ceremony["encrypted_seed"]
	}
	return value
}

func rotationMetadata(t *testing.T, conn *sql.DB, query string) []any {
	t.Helper()
	rows, err := conn.QueryContext(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Close(t, rows)
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	values := make([]any, len(columns))
	pointers := make([]any, len(columns))
	for i := range values {
		pointers[i] = &values[i]
	}
	if !rows.Next() {
		t.Fatal("missing metadata row")
	}
	if err := rows.Scan(pointers...); err != nil {
		t.Fatal(err)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return values
}
