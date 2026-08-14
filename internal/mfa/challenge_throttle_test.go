package mfa

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"durpdeploy/internal/db"
)

func TestMFARateLimit_CapsFailuresAndExpiresCooldown(t *testing.T) {
	// Given: a challenge service with a controllable clock.
	ctx := context.Background()
	service, binding, conn := newChallengeServiceForTest(t)

	// When: five factor failures are recorded against one challenge.
	for range 5 {
		if err := service.RecordFailure(ctx, binding); err != nil {
			t.Fatalf("record failure: %v", err)
		}
	}
	if err := service.RecordFailure(
		ctx,
		binding,
	); !errors.Is(
		err,
		ErrChallengeInvalid,
	) {
		t.Fatalf(
			"sixth challenge failure = %v, want %v",
			err,
			ErrChallengeInvalid,
		)
	}
	pending, err := service.Issue(ctx, ChallengeIssue{
		UserID: binding.UserID, SessionID: binding.SessionID, Purpose: binding.Purpose,
	})
	if err != nil {
		t.Fatalf("issue cooldown challenge: %v", err)
	}
	binding.Token = pending.Token
	binding.CSRF = pending.CSRF

	// Then: the account is blocked until the injected cooldown passes.
	if err := service.Consume(
		ctx,
		binding,
		func(context.Context, db.MfaChallenge) error {
			t.Fatal("blocked challenge elevated")
			return nil
		},
	); !errors.Is(
		err,
		ErrMFACooldown,
	) {
		t.Fatalf("blocked consume = %v, want %v", err, ErrMFACooldown)
	}
	service.clock = func() time.Time { return time.Unix(1_700_000_901, 0) }
	pending, err = service.Issue(ctx, ChallengeIssue{
		UserID: binding.UserID, SessionID: binding.SessionID, Purpose: binding.Purpose,
	})
	if err != nil {
		t.Fatalf("issue expired cooldown challenge: %v", err)
	}
	binding.Token = pending.Token
	binding.CSRF = pending.CSRF
	if err := service.Consume(
		ctx,
		binding,
		func(context.Context, db.MfaChallenge) error {
			return nil
		},
	); err != nil {
		t.Fatalf("consume after cooldown: %v", err)
	}
	var remaining int
	if err := conn.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM mfa_rate_limits WHERE user_id = ?",
		binding.UserID,
	).Scan(&remaining); err != nil {
		t.Fatalf("count MFA rate limits: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("MFA rate limits after success = %d, want 0", remaining)
	}
}

func TestMFARateLimit_RecordsFiveConcurrentFailuresAcrossChallenges(
	t *testing.T,
) {
	// Given: five distinct challenges for one user on multiple database connections.
	ctx := context.Background()
	service, binding, conn := newChallengeServiceForTest(t)
	conn.SetMaxOpenConns(5)
	bindings := []ChallengeBinding{binding}
	for range 4 {
		pending, err := service.Issue(ctx, ChallengeIssue{
			UserID: binding.UserID, SessionID: binding.SessionID, Purpose: binding.Purpose,
		})
		if err != nil {
			t.Fatalf("issue concurrent challenge: %v", err)
		}
		bindings = append(bindings, ChallengeBinding{
			Token: pending.Token, CSRF: pending.CSRF, UserID: binding.UserID,
			SessionID: binding.SessionID, Purpose: binding.Purpose,
		})
	}
	start := make(chan struct{})
	errs := make(chan error, len(bindings))
	var group sync.WaitGroup

	// When: every challenge records one factor failure at once.
	for _, failureBinding := range bindings {
		group.Add(1)
		go func(failureBinding ChallengeBinding) {
			defer group.Done()
			<-start
			errs <- service.RecordFailure(ctx, failureBinding)
		}(failureBinding)
	}
	close(start)
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent record failure: %v", err)
		}
	}

	// Then: all five failures persist and the next challenge is blocked.
	limit, err := db.New(conn).GetMFARateLimitByUserID(ctx, binding.UserID)
	if err != nil {
		t.Fatalf("load MFA rate limit: %v", err)
	}
	if limit.FailureCount != 5 || !limit.BlockedUntil.Valid {
		t.Fatalf("rate limit = %#v, want five failures with cooldown", limit)
	}
	pending, err := service.Issue(ctx, ChallengeIssue{
		UserID: binding.UserID, SessionID: binding.SessionID, Purpose: binding.Purpose,
	})
	if err != nil {
		t.Fatalf("issue blocked challenge: %v", err)
	}
	blocked := ChallengeBinding{
		Token: pending.Token, CSRF: pending.CSRF, UserID: binding.UserID,
		SessionID: binding.SessionID, Purpose: binding.Purpose,
	}
	if err := service.Consume(
		ctx,
		blocked,
		func(context.Context, db.MfaChallenge) error {
			t.Fatal("cooldown challenge elevated")
			return nil
		},
	); !errors.Is(
		err,
		ErrMFACooldown,
	) {
		t.Fatalf("cooldown consume = %v, want %v", err, ErrMFACooldown)
	}
}

func TestChallenge_ConsumeDoesNotReplayWhenCallbackFails(t *testing.T) {
	// Given: one guarded challenge and a callback that reports an elevation failure.
	ctx := context.Background()
	service, binding, _ := newChallengeServiceForTest(t)
	callbackErr := errors.New("elevation failed")
	callbacks := 0

	// When: the guarded consume wins but the callback returns an error.
	err := service.Consume(
		ctx,
		binding,
		func(context.Context, db.MfaChallenge) error {
			callbacks++
			return callbackErr
		},
	)

	// Then: the error is returned and the one-use challenge cannot replay.
	if !errors.Is(err, callbackErr) {
		t.Fatalf("callback error = %v, want %v", err, callbackErr)
	}
	if err := service.Consume(
		ctx,
		binding,
		func(context.Context, db.MfaChallenge) error {
			callbacks++
			return nil
		},
	); !errors.Is(
		err,
		ErrChallengeInvalid,
	) {
		t.Fatalf("replay error = %v, want %v", err, ErrChallengeInvalid)
	}
	if callbacks != 1 {
		t.Fatalf("callbacks = %d, want 1", callbacks)
	}
}
