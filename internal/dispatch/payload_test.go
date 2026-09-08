package dispatch_test

import (
	"testing"

	"durpdeploy/internal/dispatch"

	"github.com/DeveloperDurp/durpdeploy-agent/executor"
	agentpayload "github.com/DeveloperDurp/durpdeploy-agent/payload"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
	"github.com/stretchr/testify/require"
)

func TestPayload_SealsForAgent_whenSnapshotIsValid(t *testing.T) {
	// Given an immutable snapshot and the standalone agent's identity.
	identity, err := agenttls.LoadOrCreate(t.TempDir(), "https://agent.test")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := dispatch.Payload{
		DeploymentID: agentproto.DeploymentID(42),
		Release: dispatch.ReleaseSnapshot{
			ID: 7, ProjectID: 3, Version: "1.2.3",
			Steps: []executor.Step{{
				Name: "deploy", ScriptBody: "echo ready", SortOrder: 1,
				TimeoutSeconds: 30, MaxRetries: 2,
			}},
		},
		Environment: dispatch.EnvironmentSnapshot{ID: 5, Name: "test"},
		Variables: []dispatch.VariableSnapshot{
			{Name: "MODE", Value: "test", Secret: false},
		},
	}

	// When the server seals the snapshot for that agent.
	envelope, err := snapshot.Seal(identity.Certificate.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}

	// Then the module decrypts exactly the agent/1 payload, bound to its ID.
	plaintext, err := agentpayload.Open(identity, 42, envelope)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"deployment_id":42,"release":{"id":7,"project_id":3,` +
		`"version":"1.2.3","steps":[{"name":"deploy",` +
		`"script_body":"echo ready","sort_order":1,` +
		`"timeout_seconds":30,"max_retries":2}]},` +
		`"environment":{"id":5,"name":"test"},` +
		`"variables":[{"name":"MODE","value":"test","secret":false}]}`
	require.JSONEq(t, want, string(plaintext), "agent/1 payload contract")
	if _, err := agentpayload.Open(identity, 43, envelope); err == nil {
		t.Fatal("envelope accepted another deployment ID")
	}
}

func TestPayload_RejectsInvalidRecipientOrID(t *testing.T) {
	identity, err := agenttls.LoadOrCreate(t.TempDir(), "https://agent.test")
	if err != nil {
		t.Fatal(err)
	}
	certificate := identity.Certificate.Certificate[0]
	for _, id := range []agentproto.DeploymentID{0, -1} {
		if _, err := (dispatch.Payload{DeploymentID: id}).Seal(certificate); err == nil {
			t.Errorf("accepted invalid deployment ID %d", id)
		} else {
			t.Logf("valid recipient, deployment ID %d: rejected", id)
		}
	}
	if _, err := (dispatch.Payload{DeploymentID: 42}).Seal(nil); err == nil {
		t.Fatal("accepted invalid recipient for valid deployment ID")
	}
	if _, err := (dispatch.Payload{DeploymentID: 42}).Seal(certificate); err != nil {
		t.Fatalf("valid recipient and deployment ID rejected: %v", err)
	}
	t.Log(
		"deployment ID 42: invalid recipient rejected; valid recipient accepted",
	)
}
