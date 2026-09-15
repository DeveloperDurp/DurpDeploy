package repository

import (
	"bytes"
	"context"
	"sync"
	"testing"

	"durpdeploy/internal/db"
)

func TestConcurrentPollCapacityAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		engine := newDeploymentCreationEngine(t, name)
		first, second := openDeploymentCreationEngine(t, engine)
		ctx := context.Background()
		for range 2 {
			if _, err := first.CreateDeployment(ctx, db.CreateDeploymentParams{
				ReleaseID: 1, EnvironmentID: 1, Status: "pending",
			}); err != nil {
				t.Fatal(err)
			}
		}

		start := make(chan struct{})
		var wait sync.WaitGroup
		claimed := make([]bool, 2)
		errs := make([]error, 2)
		for index, repo := range []*Repository{first, second} {
			wait.Add(1)
			go func() {
				defer wait.Done()
				<-start
				_, claimed[index], errs[index] = repo.ClaimRemoteDeploymentPayload(
					ctx,
					"race-agent",
					func(snapshot RemotePayloadSnapshot) (RemotePreparedClaim, error) {
						return RemotePreparedClaim{
							TokenHash: bytes.Repeat(
								[]byte{byte(snapshot.Deployment.ID)},
								32,
							),
							Ciphertext: []byte("sealed"),
						}, nil
					},
				)
			}()
		}
		close(start)
		wait.Wait()

		if errs[0] != nil || errs[1] != nil || claimed[0] == claimed[1] {
			t.Fatalf("%s claims=%v errors=%v", name, claimed, errs)
		}
		var inFlight int
		if err := first.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM
			remote_deployment_claims WHERE state='claimed'`).Scan(&inFlight); err != nil {
			t.Fatal(err)
		}
		if inFlight != 1 {
			t.Fatalf("%s in-flight claims=%d want=1", name, inFlight)
		}
	})
}
