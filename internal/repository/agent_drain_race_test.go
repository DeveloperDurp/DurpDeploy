package repository_test

import (
	"context"
	"testing"
	"time"

	"durpdeploy/internal/repository"
)

func TestAgentDrainSerializesWithStepClaim(t *testing.T) {
	// Given: payload preparation holds the agent lock before issuing a claim.
	repo := remoteFixture(t)
	for _, statement := range []string{
		`UPDATE deployments SET status='running' WHERE id=3`,
		`INSERT INTO agent_labels VALUES ('a','other')`,
		`INSERT INTO agent_environment_labels(agent_id,environment_id) VALUES ('a',2)`,
		`INSERT INTO remote_step_runs
		 (deployment_id,step_index,agent_id) VALUES (3,0,'a')`,
	} {
		if _, err := repo.DB.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	preparing, release := make(chan struct{}), make(chan struct{})
	claimed := make(chan error, 1)
	go func() {
		_, ok, err := repo.ClaimRemoteStepPayload(ctx, "a", func(
			repository.RemotePayloadSnapshot,
		) (repository.RemotePreparedClaim, error) {
			close(preparing)
			select {
			case <-release:
			case <-ctx.Done():
				return repository.RemotePreparedClaim{}, ctx.Err()
			}
			return repository.RemotePreparedClaim{
				TokenHash: make([]byte, 32), Ciphertext: []byte("ciphertext"),
			}, nil
		})
		if err == nil && !ok {
			err = repository.ErrAgentUnavailable
		}
		claimed <- err
	}()
	select {
	case <-preparing:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// When: drain races the claim already being prepared.
	drained := make(chan error, 1)
	go func() {
		_, err := repo.SetAgentDraining(ctx, "a", true)
		drained <- err
	}()
	close(release)
	for _, done := range []chan error{claimed, drained} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	// Then: the issued claim remains usable; new claims are blocked.
	identity := repository.RemoteLifecycleClaim{
		DeploymentID: 3, AgentID: "a", ClaimTokenHash: make([]byte, 32),
	}
	if _, err := repo.StartRemoteStep(ctx, identity); err != nil {
		t.Fatal(err)
	}
	if rows, err := repo.ClaimRemoteDeployment(
		ctx, currentClaimArg(t, repo),
	); err != nil || rows != 0 {
		t.Fatalf("new claim rows=%d err=%v", rows, err)
	}
}
