package main

import (
	"fmt"
	"path/filepath"
	"testing"

	"durpdeploy/internal/migrate"
	"durpdeploy/internal/oidc"
	"durpdeploy/internal/oidc/oidctest"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/secret"
)

func TestNewOIDCServices_LeavesOIDCDisabled_when_config_disabled(t *testing.T) {
	// Given
	services, err := newOIDCServices(oidcServicesConfig{})
	if err != nil {
		t.Fatalf("construct disabled OIDC services: %v", err)
	}

	// When
	enabled := services.enabled

	// Then
	if enabled || services.provider != nil || services.transactions != nil {
		t.Fatalf("disabled OIDC services = %#v, want empty services", services)
	}
}

func TestNewOIDCServices_DoesNotDiscover_when_constructed(t *testing.T) {
	// Given
	fixture, err := oidctest.New(oidctest.Options{})
	if err != nil {
		t.Fatalf("new OIDC fixture: %v", err)
	}
	t.Cleanup(fixture.Close)

	dsn := fmt.Sprintf(
		"file:%s?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)",
		filepath.Join(t.TempDir(), "oidc.db"),
	)
	conn, err := migrate.Run(dsn)
	if err != nil {
		t.Fatalf("migrate OIDC database: %v", err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close OIDC database: %v", err)
		}
	})

	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatalf("new transaction secret box: %v", err)
	}

	// When
	services, err := newOIDCServices(oidcServicesConfig{
		Config: oidc.Config{
			Enabled:       true,
			Issuer:        fixture.URL(),
			ClientID:      fixture.ClientID(),
			ClientSecret:  fixture.ClientSecret(),
			CallbackURL:   "https://deploy.example/login/oidc/callback",
			Scopes:        []string{"openid", "profile", "email"},
			GroupClaim:    "groups",
			AdminGroup:    "fixture-admin",
			DeployerGroup: "fixture-deployer",
			ViewerGroup:   "fixture-viewer",
		},
		Repository:   repository.New(conn),
		Box:          box,
		HTTPClient:   fixture.Client(),
		CookieSecure: true,
		Now:          fixture.Now,
	})
	if err != nil {
		t.Fatalf("construct OIDC services: %v", err)
	}

	// Then
	if !services.enabled || services.provider == nil ||
		services.transactions == nil {
		t.Fatalf(
			"enabled OIDC services = %#v, want provider and store",
			services,
		)
	}
	if got := fixture.Counters().Discovery; got != 0 {
		t.Fatalf("OIDC discovery requests at startup = %d, want 0", got)
	}
}
