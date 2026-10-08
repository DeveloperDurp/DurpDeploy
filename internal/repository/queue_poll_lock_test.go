package repository

import (
	"context"
	"testing"
	"time"
)

func TestEnvironmentQueueSQLServerConcurrentPollDuringPayload(t *testing.T) {
	first, second := openDeploymentCreationEngine(t,
		newDeploymentCreationEngine(t, "SQLServer"))
	for range 2 {
		createLegacyRemoteDeployment(t, first)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	ready, resume := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-resume:
		default:
			close(resume)
		}
	}()
	type result struct {
		claimed bool
		err     error
	}
	done := make(chan result, 2)
	prepared := RemotePreparedClaim{
		TokenHash: make([]byte, 32), Ciphertext: []byte("sealed"),
	}
	go func() {
		_, claimed, err := first.ClaimRemoteDeploymentPayload(
			ctx,
			"race-agent",
			func(RemotePayloadSnapshot) (RemotePreparedClaim, error) {
				close(ready)
				select {
				case <-resume:
					return prepared, nil
				case <-ctx.Done():
					return RemotePreparedClaim{}, ctx.Err()
				}
			},
		)
		done <- result{claimed, err}
	}()
	select {
	case <-ready:
	case result := <-done:
		t.Fatalf("first payload did not start: %+v", result)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	go func() {
		_, claimed, err := second.ClaimRemoteDeploymentPayload(
			ctx,
			"race-agent",
			func(RemotePayloadSnapshot) (RemotePreparedClaim, error) {
				return prepared, nil
			},
		)
		done <- result{claimed, err}
	}()
	// Observe the competing poll waiting before letting the writer update state.
	for {
		var waiting int
		if err := first.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sys.dm_exec_requests
WHERE database_id=DB_ID() AND wait_type LIKE 'LCK_M%'`).
			Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		select {
		case result := <-done:
			t.Fatalf("second poll did not compete: %+v", result)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	close(resume)
	var claims int
	for range 2 {
		result := <-done
		if result.err != nil {
			t.Fatalf("concurrent poll failed: %v", result.err)
		}
		if result.claimed {
			claims++
		}
	}
	if claims != 1 {
		t.Fatalf("agent capacity admitted %d claims, want 1", claims)
	}
}
