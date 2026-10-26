package repository

import (
	"database/sql"
	"testing"

	"durpdeploy/internal/db"
)

func TestAPITokenUsageAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		repo, _ := openDeploymentCreationEngine(
			t,
			newDeploymentCreationEngine(t, name),
		)
		user, err := repo.Queries.CreateUser(t.Context(), db.CreateUserParams{
			Email: "usage@example.test", Name: "Usage", Role: "admin",
			PasswordHash: "unused",
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = repo.Queries.CreateApiToken(
			t.Context(),
			db.CreateApiTokenParams{
				ID: "usage", UserID: user.ID, Name: "Usage",
				TokenPrefix: "fixture", TokenHash: "fixture", Scope: "global",
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		for _, test := range []struct{ observed, want int64 }{
			{100, 100}, {159, 100}, {160, 160}, {120, 160},
			{220, 220}, {500, 500}, {300, 500}, {559, 500}, {560, 560},
		} {
			if err := repo.Queries.TouchApiTokenLastUsed(t.Context(), db.TouchApiTokenLastUsedParams{
				ID: "usage", LastUsedAt: sql.NullInt64{Int64: test.observed, Valid: true},
				StaleBefore: sql.NullInt64{Int64: test.observed - 60, Valid: true},
			}); err != nil {
				t.Fatal(err)
			}
			token, err := repo.Queries.GetApiTokenByID(t.Context(), "usage")
			if err != nil || !token.LastUsedAt.Valid ||
				token.LastUsedAt.Int64 != test.want {
				t.Fatalf(
					"observed=%d timestamp=%+v want=%d error=%v",
					test.observed,
					token.LastUsedAt,
					test.want,
					err,
				)
			}
		}
		if _, err := repo.DB.ExecContext(t.Context(),
			"UPDATE api_tokens SET revoked_at = ? WHERE id = ?", 1, "usage",
		); err != nil {
			t.Fatal(err)
		}
		if err := repo.Queries.TouchApiTokenLastUsed(t.Context(), db.TouchApiTokenLastUsedParams{
			ID: "usage", LastUsedAt: sql.NullInt64{Int64: 1000, Valid: true},
			StaleBefore: sql.NullInt64{Int64: 940, Valid: true},
		}); err != nil {
			t.Fatal(err)
		}
		var lastUsed int64
		if err := repo.DB.QueryRowContext(t.Context(), "SELECT last_used_at FROM api_tokens WHERE id = ?", "usage").Scan(&lastUsed); err != nil ||
			lastUsed != 560 {
			t.Fatalf("revoked timestamp=%d error=%v", lastUsed, err)
		}
	})
}
