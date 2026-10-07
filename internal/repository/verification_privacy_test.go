package repository

import (
	"database/sql"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/secret"
)

func TestVerificationNeverDecryptedInLegacyAgentPayload(t *testing.T) {
	repo, _ := openDeploymentCreationEngine(t,
		newDeploymentCreationEngine(t, "SQLite"))
	deployment := createLegacyRemoteDeployment(t, repo)
	if _, err := repo.Queries.AddAgentInterpreter(t.Context(), db.AddAgentInterpreterParams{
		AgentID: "race-agent", Interpreter: "bash",
	}); err != nil {
		t.Fatal(err)
	}
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	repo.SetSecretBox(box)
	target, err := box.Encrypt("echo administrator-private-check")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SnapshotDeploymentSteps(
		t.Context(),
		deployment.ID,
		[]DeploymentStepSnapshot{
			{CreateDeploymentStepParams: db.CreateDeploymentStepParams{
				Name:            "verification",
				ExecutionTarget: "agent",
				Interpreter:     "bash",
			}},
		},
	); err != nil {
		t.Fatal(err)
	}
	if err := repo.Queries.CreateDeploymentVerification(t.Context(),
		db.CreateDeploymentVerificationParams{
			DeploymentID: deployment.ID, Type: "bash", Target: target,
			TimeoutSeconds: 10, StepIndex: 0,
		}); err != nil {
		t.Fatal(err)
	}
	_, claimed, err := repo.ClaimRemoteDeploymentPayload(
		t.Context(),
		"race-agent",
		func(snapshot RemotePayloadSnapshot) (RemotePreparedClaim, error) {
			if len(snapshot.Steps) != 1 || snapshot.Steps[0].ScriptBody != "" {
				t.Fatal(
					"legacy agent payload received a decrypted check script",
				)
			}
			return RemotePreparedClaim{TokenHash: make([]byte, 32),
				Ciphertext: []byte("fixture-ciphertext")}, nil
		},
	)
	if err != nil || !claimed {
		t.Fatalf("legacy claim=%v error=%v", claimed, err)
	}
	// An old all-agent snapshot now runs verification locally. Recovery must
	// retain its cleanup block when the previous namespace cannot be swept.
	if _, err := repo.DB.ExecContext(t.Context(),
		`UPDATE deployments SET assigned_agent_id=NULL, status='running',
		container_namespace='previous-runtime' WHERE id=?`, deployment.ID,
	); err != nil {
		t.Fatal(err)
	}
	marked, err := repo.Queries.MarkUnreconciledLocalDeployments(t.Context(),
		sql.NullInt64{Int64: 100, Valid: true})
	if err != nil || marked != 1 {
		t.Fatalf(
			"unreconciled verification containers=%d error=%v",
			marked,
			err,
		)
	}
}
