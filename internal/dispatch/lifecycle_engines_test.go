package dispatch

import (
	"bytes"
	"errors"
	"sync"
	"testing"

	"durpdeploy/internal/repository"
)

func TestStartMaintenanceExactDeadlineAcrossDatabases(t *testing.T) {
	for _, name := range []string{"SQLite", "PostgreSQL", "SQLServer"} {
		t.Run(name, func(t *testing.T) {
			engine := provisionLifecycleEngine(t, name)
			first, second := openLifecycleEngine(t, engine)
			seedExactDeadlineClaim(t, first)
			identity := repository.RemoteLifecycleClaim{
				DeploymentID:   1,
				AgentID:        "race-agent",
				ClaimTokenHash: bytes.Repeat([]byte{1}, 32),
			}
			start := make(chan struct{})
			var wait sync.WaitGroup
			wait.Add(2)
			var startErr, maintainErr error
			go func() {
				defer wait.Done()
				<-start
				startErr = second.StartRemoteDeployment(t.Context(), identity)
			}()
			go func() {
				defer wait.Done()
				<-start
				maintainErr = New(first).Maintain(t.Context())
			}()
			close(start)
			wait.Wait()
			if maintainErr != nil || !errors.Is(
				startErr,
				repository.ErrRemoteLifecycleConflict,
			) {
				t.Fatalf("start error=%v maintenance error=%v",
					startErr, maintainErr)
			}
			claim, err := first.Queries.GetRemoteDeploymentClaim(t.Context(), 1)
			if err != nil || claim.State != "waiting" || claim.StartedAt.Valid {
				t.Fatalf("deadline claim=%+v error=%v", claim, err)
			}
		})
	}
}
