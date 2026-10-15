package oidc

import (
	"testing"
	"time"

	"durpdeploy/internal/db"
)

func TestMaintainerGroupConfiguration(t *testing.T) {
	for _, group := range []string{"maintainers", "", " ", "durpdeploy-admin", "durpdeploy-deployer", "durpdeploy-viewer"} {
		t.Run("group="+group, func(t *testing.T) {
			clearOIDCEnv(t)
			for key, value := range completeEnv() {
				t.Setenv(key, value)
			}
			t.Setenv("DURPDEPLOY_OIDC_MAINTAINER_GROUP", group)
			config, err := LoadConfig()
			if group != "maintainers" {
				if err == nil {
					t.Fatal("empty or overlapping maintainer group accepted")
				}
				return
			}
			if err != nil || config.MaintainerGroup != group {
				t.Fatalf("maintainer mapping: %v", err)
			}
		})
	}
}

func TestMaintainerOIDCSynchronization(t *testing.T) {
	fixture := newProviderFixture(t)
	fixture.config.GroupClaim = "groups"
	fixture.config.AdminGroup = "admin"
	fixture.config.MaintainerGroup = "maintainer"
	fixture.config.DeployerGroup = "deployer"
	fixture.config.ViewerGroup = "viewer"
	provider := fixture.newProvider(t)
	repo := newIdentityRepository(t)
	for _, group := range []string{"maintainer", "deployer"} {
		identity, err := provider.ParseClaims([]byte(`{"sub":"role-person",` +
			`"email":"role-person@example.test","email_verified":true,` +
			`"groups":["viewer","deployer","` + group + `"]}`))
		if err != nil {
			t.Fatal(err)
		}
		user, err := ResolveIdentity(t.Context(), repo, identity)
		if err != nil || user.Role != group {
			t.Fatalf("synchronized role=%q, error=%v", user.Role, err)
		}
		if group == "maintainer" {
			_, err = repo.Queries.CreateSession(
				t.Context(),
				db.CreateSessionParams{
					ID: "maintainer-session", UserID: user.ID, CsrfToken: "csrf",
					ExpiresAt: time.Now().Add(time.Hour).Unix(),
				},
			)
			if err != nil {
				t.Fatal(err)
			}
		} else {
			if _, err := repo.Queries.GetSession(t.Context(), db.GetSessionParams{
				ID: "maintainer-session", ExpiresAt: time.Now().Unix(),
			}); err == nil {
				t.Fatal("OIDC demotion retained the authoring session")
			}
		}
	}
}
