package repository

import (
	"errors"
	"testing"

	"durpdeploy/internal/db"
)

func TestTokenMintBackendParity(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, engine string) {
		r, _ := openDeploymentCreationEngine(t,
			newDeploymentCreationEngine(t, engine))
		user, err := r.Queries.CreateUser(t.Context(), db.CreateUserParams{
			Email: "mint@example.test", Name: "Mint", PasswordHash: "unused",
			Role: "maintainer",
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			global, member, ceiling string
			allowed                 bool
		}{
			{"maintainer", "admin", "admin", true},
			{"maintainer", "maintainer", "admin", false},
			{"maintainer", "maintainer", "maintainer", true},
			{"maintainer", "viewer", "deployer", false},
			{"maintainer", "viewer", "viewer", true},
			{"deployer", "admin", "admin", false},
			{"deployer", "admin", "maintainer", false},
			{"deployer", "admin", "deployer", true},
			{"viewer", "admin", "viewer", true},
			{"viewer", "admin", "deployer", false},
			{"admin", "viewer", "admin", true},
		} {
			t.Run(tc.global+"/"+tc.member+"/"+tc.ceiling, func(t *testing.T) {
				user.Role = tc.global
				if err := r.Queries.AddProjectMember(t.Context(),
					db.AddProjectMemberParams{ProjectID: 1, UserID: user.ID,
						Role: tc.member}); err != nil {
					t.Fatal(err)
				}
				_, err := r.ValidateTokenMint(t.Context(), user, TokenScope{
					Scope: "scoped", Role: tc.ceiling, ProjectIDs: []int64{1},
				})
				if tc.allowed && err != nil || !tc.allowed &&
					!errors.Is(err, ErrTokenRoleCeiling) {
					t.Fatalf("allowed=%t, error=%v", tc.allowed, err)
				}
			})
		}
	})
}
