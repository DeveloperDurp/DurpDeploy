package auth

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"durpdeploy/internal/config"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

type apiTokenTelemetry struct {
	mu      sync.Mutex
	pending map[string]struct{}
	limits  config.TokenTelemetryLimits
}

func (t *apiTokenTelemetry) touch(
	repo *repository.Repository, token db.GetApiTokenByHashRow,
) {
	now := time.Now().Unix()
	if token.LastUsedAt.Valid &&
		token.LastUsedAt.Int64 > now-t.limits.IntervalSeconds {
		return
	}
	t.mu.Lock()
	_, pending := t.pending[token.ID]
	// ponytail: skip telemetry at capacity; add a bounded queue if accuracy needs it.
	if pending || int64(len(t.pending)) >= t.limits.Concurrency {
		t.mu.Unlock()
		return
	}
	t.pending[token.ID] = struct{}{}
	t.mu.Unlock()
	go func() {
		defer func() {
			t.mu.Lock()
			delete(t.pending, token.ID)
			t.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(),
			time.Duration(t.limits.WriteTimeoutSeconds)*time.Second)
		defer cancel()
		_ = repo.Queries.TouchApiTokenLastUsed(
			ctx,
			db.TouchApiTokenLastUsedParams{
				ID:         token.ID,
				LastUsedAt: sql.NullInt64{Int64: now, Valid: true},
				StaleBefore: sql.NullInt64{
					Int64: now - t.limits.IntervalSeconds, Valid: true,
				},
			},
		)
	}()
}
