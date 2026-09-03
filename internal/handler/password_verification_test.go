package handler

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"durpdeploy/internal/db"
)

func TestLoginPasswordHashUsesDummyForPasswordlessAccount(t *testing.T) {
	passwordHash, hasPassword := loginPasswordHash(db.User{}, nil)
	if hasPassword {
		t.Fatal("passwordless account reported a password")
	}
	if passwordHash != unknownAccountPasswordHash {
		t.Fatal("passwordless account skipped the dummy hash")
	}
}

func TestPasswordVerificationBoundsConcurrentWork(t *testing.T) {
	handler := NewAuthHandler(nil)
	ready := make(chan struct{}, 8)
	start := make(chan struct{})
	release := make(chan struct{})
	entered := make(chan struct{}, 8)
	var inFlight atomic.Int32
	var peak atomic.Int32
	var workers sync.WaitGroup

	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			ready <- struct{}{}
			<-start
			_, err := handler.withPasswordVerification(
				context.Background(),
				func() bool {
					current := inFlight.Add(1)
					for {
						previous := peak.Load()
						if current <= previous ||
							peak.CompareAndSwap(previous, current) {
							break
						}
					}
					entered <- struct{}{}
					<-release
					inFlight.Add(-1)
					return false
				},
			)
			if err != nil {
				t.Errorf("password verification failed: %v", err)
			}
		}()
	}

	for range 8 {
		<-ready
	}
	close(start)
	<-entered
	<-entered
	select {
	case <-entered:
		t.Error("more than two password verifications ran concurrently")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	workers.Wait()

	if got := peak.Load(); got != 2 {
		t.Fatalf("peak password verifications = %d, want 2", got)
	}
}

func TestPasswordVerificationWaitHonorsCancellation(t *testing.T) {
	handler := &AuthHandler{passwordVerifications: make(chan struct{}, 1)}
	handler.passwordVerifications <- struct{}{}

	ctx, cancel := context.WithCancel(context.Background())
	waiting := make(chan struct{})
	result := make(chan error, 1)
	var verified atomic.Bool
	go func() {
		_, err := handler.withPasswordVerification(
			&observedDoneContext{Context: ctx, waiting: waiting},
			func() bool {
				verified.Store(true)
				return false
			},
		)
		result <- err
	}()

	<-waiting
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("password verification error = %v, want context canceled", err)
	}
	if verified.Load() {
		t.Fatal("password verification ran while waiting request was canceled")
	}
	if got := len(handler.passwordVerifications); got != 1 {
		t.Fatalf("password verification slots = %d, want 1", got)
	}

	<-handler.passwordVerifications
	if _, err := handler.withPasswordVerification(
		context.Background(),
		func() bool { return false },
	); err != nil {
		t.Fatalf("password verification failed: %v", err)
	}
	if got := len(handler.passwordVerifications); got != 0 {
		t.Fatalf("released password verification slots = %d, want 0", got)
	}
}

type observedDoneContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *observedDoneContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}
