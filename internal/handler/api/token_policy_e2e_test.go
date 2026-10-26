package api_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"durpdeploy/internal/config"
)

func TestConfiguredTokenTelemetryE2E(t *testing.T) {
	for _, capacity := range []int64{2, 12} {
		t.Run(fmt.Sprintf("capacity %d", capacity), func(t *testing.T) {
			testTokenPolicyCapacity(t, capacity)
		})
	}
	for _, interval := range []int64{10, 180} {
		t.Run(fmt.Sprintf("interval %d", interval), func(t *testing.T) {
			settings := config.Defaults()
			settings.Limits.TokenTelemetry.IntervalSeconds = interval
			f := newConfiguredTokenUsageFixture(t, false, settings)
			close(f.probe.release)
			setTokenUsageAge(t, f, interval/2)
			f.get(t, f.token, "/api/v1/users/me", 200)
			if f.probe.calls.Load() != 0 {
				t.Fatal("recent usage bypassed the configured interval")
			}
			setTokenUsageAge(t, f, interval+1)
			f.get(t, f.token, "/api/v1/users/me", 200)
			if err := waitTokenUsageWrite(t, f.probe); err != nil {
				t.Fatal(err)
			}
			row, err := f.repo.Queries.GetApiTokenByID(t.Context(), f.tokenID)
			if err != nil || !row.LastUsedAt.Valid ||
				row.LastUsedAt.Int64 < time.Now().Unix()-2 {
				t.Fatal(
					"stale usage did not update under the configured policy",
				)
			}
			f.checkUsageReports(t, row.LastUsedAt.Int64)
		})
	}
	t.Run("independent routers", testTokenPolicyIsolation)
	for _, seconds := range []int64{1, 7} {
		t.Run(fmt.Sprintf("deadline %d", seconds), func(t *testing.T) {
			testTokenPolicyDeadline(t, seconds)
		})
	}
}

func testTokenPolicyCapacity(t *testing.T, capacity int64) {
	t.Helper()
	settings := config.Defaults()
	settings.Limits.TokenTelemetry.Concurrency = capacity
	f := newConfiguredTokenUsageFixture(t, false, settings)
	for range capacity {
		_, token := seedAPIToken(t, f.repo, f.userID)
		f.get(t, token, "/api/v1/users/me", 200)
	}
	waitTokenUsage(t, func() bool {
		return f.probe.active.Load() == capacity
	})
	f.burst(t, []string{f.token, f.token, f.token})
	if f.probe.calls.Load() != capacity ||
		f.probe.peak.Load() != capacity {
		t.Fatal("telemetry did not enforce the configured capacity")
	}
	if err := f.repo.Queries.RevokeApiToken(
		t.Context(), f.tokenID,
	); err != nil {
		t.Fatal(err)
	}
	f.get(t, f.token, "/api/v1/users/me", 401)
	close(f.probe.release)
	for range capacity {
		if err := waitTokenUsageWrite(t, f.probe); err != nil {
			t.Fatal(err)
		}
	}
}

func setTokenUsageAge(t *testing.T, f *tokenUsageFixture, age int64) {
	t.Helper()
	if _, err := f.repo.DB.ExecContext(t.Context(),
		"UPDATE api_tokens SET last_used_at = ? WHERE id = ?",
		time.Now().Unix()-age, f.tokenID,
	); err != nil {
		t.Fatal(err)
	}
}

func testTokenPolicyIsolation(t *testing.T) {
	settings := config.Defaults()
	settings.Limits.TokenTelemetry.Concurrency = 1
	first := newConfiguredTokenUsageFixture(t, false, settings)
	settings.Limits.TokenTelemetry.Concurrency = 3
	second := newConfiguredTokenUsageFixture(t, false, settings)
	first.get(t, first.token, "/api/v1/users/me", 200)
	second.get(t, second.token, "/api/v1/users/me", 200)
	for range 2 {
		_, token := seedAPIToken(t, second.repo, second.userID)
		second.get(t, token, "/api/v1/users/me", 200)
	}
	_, overflow := seedAPIToken(t, first.repo, first.userID)
	first.get(t, overflow, "/api/v1/users/me", 200)
	waitTokenUsage(t, func() bool {
		return second.probe.active.Load() == 3
	})
	if first.probe.calls.Load() != 1 || first.probe.peak.Load() != 1 {
		t.Fatal("another instance changed the first router's policy")
	}
	close(first.probe.release)
	close(second.probe.release)
	if err := waitTokenUsageWrite(t, first.probe); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := waitTokenUsageWrite(t, second.probe); err != nil {
			t.Fatal(err)
		}
	}
}

func testTokenPolicyDeadline(t *testing.T, seconds int64) {
	t.Helper()
	settings := config.Defaults()
	settings.Limits.TokenTelemetry = config.TokenTelemetryLimits{
		Concurrency: 1, IntervalSeconds: 120, WriteTimeoutSeconds: seconds,
	}
	f := newConfiguredTokenUsageFixture(t, false, settings)
	f.get(t, f.token, "/api/v1/users/me", 200)
	select {
	case ctx := <-f.probe.contexts:
		deadline, ok := ctx.Deadline()
		budget := time.Duration(seconds) * time.Second
		remaining := time.Until(deadline)
		if !ok || remaining > budget || remaining < budget-time.Second {
			t.Fatal("telemetry ignored its configured write deadline")
		}
	case <-time.After(time.Second):
		t.Fatal("telemetry did not start")
	}
	if seconds > 1 {
		close(f.probe.release)
		if err := waitTokenUsageWrite(t, f.probe); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := waitTokenUsageWrite(t, f.probe); !errors.Is(
		err, context.DeadlineExceeded,
	) {
		t.Fatalf("blocked telemetry error: %v", err)
	}
	close(f.probe.release)
	waitTokenUsage(t, func() bool {
		f.get(t, f.token, "/api/v1/users/me", 200)
		row, err := f.repo.Queries.GetApiTokenByID(t.Context(), f.tokenID)
		return err == nil && row.LastUsedAt.Valid
	})
	if f.probe.peak.Load() != 1 {
		t.Fatal("deadline recovery exceeded the configured capacity")
	}
}
