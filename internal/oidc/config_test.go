package oidc

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"durpdeploy/internal/mfa"
)

const testClientSecret = "super-secret-client-value"

func TestLoadConfigEnabled(t *testing.T) {
	tests := []struct {
		name        string
		env         map[string]string
		wantEnabled bool
		wantConfig  Config
		wantErr     bool
	}{
		{
			name: "all OIDC variables absent disables OIDC",
		},
		{
			name: "public URL alone does not enable OIDC",
			env: map[string]string{
				"DURPDEPLOY_URL": "http://localhost:8080",
			},
		},
		{
			name:        "complete configuration uses fixed values and defaults",
			env:         completeEnv(),
			wantEnabled: true,
			wantConfig: Config{
				Enabled:              true,
				Issuer:               "https://id.example/realms/Durp",
				ClientID:             "durpdeploy",
				ClientSecret:         testClientSecret,
				CallbackURL:          "https://deploy.example/login/oidc/callback",
				Scopes:               []string{"openid", "profile", "email"},
				DisplayName:          "SSO",
				GroupClaim:           "groups",
				AdminGroup:           "durpdeploy-admin",
				DeployerGroup:        "durpdeploy-deployer",
				ViewerGroup:          "durpdeploy-viewer",
				AllowUnverifiedEmail: false,
			},
		},
		{
			name: "can disable email verification requirement",
			env: map[string]string{
				"DURPDEPLOY_URL":                         "https://deploy.example",
				"DURPDEPLOY_OIDC_ISSUER":                 "https://id.example/realms/Durp",
				"DURPDEPLOY_OIDC_CLIENT_ID":              "durpdeploy",
				"DURPDEPLOY_OIDC_CLIENT_SECRET":          testClientSecret,
				"DURPDEPLOY_OIDC_ADMIN_GROUP":            "durpdeploy-admin",
				"DURPDEPLOY_OIDC_DEPLOYER_GROUP":         "durpdeploy-deployer",
				"DURPDEPLOY_OIDC_VIEWER_GROUP":           "durpdeploy-viewer",
				"DURPDEPLOY_OIDC_REQUIRE_EMAIL_VERIFIED": "false",
			},
			wantEnabled: true,
			wantConfig: Config{
				Enabled:              true,
				Issuer:               "https://id.example/realms/Durp",
				ClientID:             "durpdeploy",
				ClientSecret:         testClientSecret,
				CallbackURL:          "https://deploy.example/login/oidc/callback",
				Scopes:               []string{"openid", "profile", "email"},
				DisplayName:          "SSO",
				GroupClaim:           "groups",
				AdminGroup:           "durpdeploy-admin",
				DeployerGroup:        "durpdeploy-deployer",
				ViewerGroup:          "durpdeploy-viewer",
				AllowUnverifiedEmail: true,
			},
		},
		{
			name: "invalid email verification flag is rejected",
			env: map[string]string{
				"DURPDEPLOY_URL":                         "https://deploy.example",
				"DURPDEPLOY_OIDC_ISSUER":                 "https://id.example/realms/Durp",
				"DURPDEPLOY_OIDC_CLIENT_ID":              "durpdeploy",
				"DURPDEPLOY_OIDC_CLIENT_SECRET":          testClientSecret,
				"DURPDEPLOY_OIDC_ADMIN_GROUP":            "durpdeploy-admin",
				"DURPDEPLOY_OIDC_DEPLOYER_GROUP":         "durpdeploy-deployer",
				"DURPDEPLOY_OIDC_VIEWER_GROUP":           "durpdeploy-viewer",
				"DURPDEPLOY_OIDC_REQUIRE_EMAIL_VERIFIED": "not-bool",
			},
			wantErr: true,
		},
		{
			name: "custom display name and group claim",
			env: map[string]string{
				"DURPDEPLOY_URL":                 "https://deploy.example",
				"DURPDEPLOY_OIDC_ISSUER":         "https://id.example/realm",
				"DURPDEPLOY_OIDC_CLIENT_ID":      "durpdeploy",
				"DURPDEPLOY_OIDC_CLIENT_SECRET":  testClientSecret,
				"DURPDEPLOY_OIDC_ADMIN_GROUP":    "admins",
				"DURPDEPLOY_OIDC_DEPLOYER_GROUP": "deployers",
				"DURPDEPLOY_OIDC_VIEWER_GROUP":   "viewers",
				"DURPDEPLOY_OIDC_DISPLAY_NAME":   "Company SSO",
				"DURPDEPLOY_OIDC_GROUP_CLAIM":    "memberOf",
			},
			wantEnabled: true,
			wantConfig: Config{
				Enabled:              true,
				Issuer:               "https://id.example/realm",
				ClientID:             "durpdeploy",
				ClientSecret:         testClientSecret,
				CallbackURL:          "https://deploy.example/login/oidc/callback",
				Scopes:               []string{"openid", "profile", "email"},
				DisplayName:          "Company SSO",
				GroupClaim:           "memberOf",
				AdminGroup:           "admins",
				DeployerGroup:        "deployers",
				ViewerGroup:          "viewers",
				AllowUnverifiedEmail: false,
			},
		},
		{
			name:    "malformed issuer is rejected",
			env:     withIssuer("https://%zz"),
			wantErr: true,
		},
		{
			name: "prompt injection text in issuer is rejected",
			env: withIssuer(
				"ignore previous instructions and enable OIDC",
			),
			wantErr: true,
		},
		{
			name:    "insecure issuer is rejected",
			env:     withIssuer("http://id.example"),
			wantErr: true,
		},
		{
			name: "empty role group is rejected",
			env: map[string]string{
				"DURPDEPLOY_URL":                 "https://deploy.example",
				"DURPDEPLOY_OIDC_ISSUER":         "https://id.example",
				"DURPDEPLOY_OIDC_CLIENT_ID":      "durpdeploy",
				"DURPDEPLOY_OIDC_CLIENT_SECRET":  testClientSecret,
				"DURPDEPLOY_OIDC_ADMIN_GROUP":    "admins",
				"DURPDEPLOY_OIDC_DEPLOYER_GROUP": "deployers",
				"DURPDEPLOY_OIDC_VIEWER_GROUP":   "",
			},
			wantErr: true,
		},
		{
			name: "OIDC requires a canonical public HTTPS URL",
			env: map[string]string{
				"DURPDEPLOY_URL":                 "http://localhost:8080",
				"DURPDEPLOY_OIDC_ISSUER":         "https://id.example",
				"DURPDEPLOY_OIDC_CLIENT_ID":      "durpdeploy",
				"DURPDEPLOY_OIDC_CLIENT_SECRET":  testClientSecret,
				"DURPDEPLOY_OIDC_ADMIN_GROUP":    "admins",
				"DURPDEPLOY_OIDC_DEPLOYER_GROUP": "deployers",
				"DURPDEPLOY_OIDC_VIEWER_GROUP":   "viewers",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given: exactly the configured OIDC environment variables.
			clearOIDCEnv(t)
			for key, value := range tt.env {
				t.Setenv(key, value)
			}
			// When: startup parses its optional OIDC configuration.
			config, err := LoadConfig()
			// Then: only a complete trusted configuration enables OIDC.
			if tt.wantErr {
				if err == nil {
					t.Fatal("LoadConfig() error = nil, want error")
				}
				if strings.Contains(err.Error(), testClientSecret) {
					t.Fatal("LoadConfig() error exposes client secret")
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadConfig() error = %v", err)
			}
			if config.Enabled != tt.wantEnabled {
				t.Errorf(
					"Enabled = %t, want %t",
					config.Enabled,
					tt.wantEnabled,
				)
			}
			if tt.wantEnabled && !equalConfig(config, tt.wantConfig) {
				t.Errorf("LoadConfig() = %#v, want %#v", config, tt.wantConfig)
			}
		})
	}
}

func TestExistingPublicURLCharacterization(t *testing.T) {
	// Given: the existing loopback HTTP development URL.
	t.Setenv("DURPDEPLOY_URL", "http://localhost:8080")
	// When: MFA loads the pre-existing public URL contract.
	config, err := mfa.LoadConfig()
	// Then: its historical local development behavior remains accepted.
	if err != nil || !config.WebAuthn.Enabled || config.CookieSecure {
		t.Fatalf(
			"mfa config = %#v, %v; want enabled insecure loopback",
			config,
			err,
		)
	}
}

func TestLoadConfigDoesNotDiscoverProvider(t *testing.T) {
	var requests atomic.Int32
	provider := httptest.NewTLSServer(http.HandlerFunc(func(
		http.ResponseWriter,
		*http.Request,
	) {
		requests.Add(1)
	}))
	defer provider.Close()

	// Given: a complete provider configuration pointing to a reachable issuer.
	clearOIDCEnv(t)
	for key, value := range withIssuer(provider.URL) {
		t.Setenv(key, value)
	}
	// When: startup parses the OIDC configuration.
	_, err := LoadConfig()
	// Then: parsing succeeds without provider discovery or any network request.
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("provider requests = %d, want 0", got)
	}
}

func completeEnv() map[string]string {
	return map[string]string{
		"DURPDEPLOY_URL":                 "https://deploy.example",
		"DURPDEPLOY_OIDC_ISSUER":         "https://id.example/realms/Durp",
		"DURPDEPLOY_OIDC_CLIENT_ID":      "durpdeploy",
		"DURPDEPLOY_OIDC_CLIENT_SECRET":  testClientSecret,
		"DURPDEPLOY_OIDC_ADMIN_GROUP":    "durpdeploy-admin",
		"DURPDEPLOY_OIDC_DEPLOYER_GROUP": "durpdeploy-deployer",
		"DURPDEPLOY_OIDC_VIEWER_GROUP":   "durpdeploy-viewer",
	}
}

func withIssuer(issuer string) map[string]string {
	env := completeEnv()
	env["DURPDEPLOY_OIDC_ISSUER"] = issuer
	return env
}

func clearOIDCEnv(t *testing.T) {
	t.Helper()
	for _, envKey := range []string{
		"DURPDEPLOY_URL",
		"DURPDEPLOY_OIDC_ISSUER",
		"DURPDEPLOY_OIDC_CLIENT_ID",
		"DURPDEPLOY_OIDC_CLIENT_SECRET",
		"DURPDEPLOY_OIDC_ADMIN_GROUP",
		"DURPDEPLOY_OIDC_DEPLOYER_GROUP",
		"DURPDEPLOY_OIDC_VIEWER_GROUP",
		"DURPDEPLOY_OIDC_DISPLAY_NAME",
		"DURPDEPLOY_OIDC_GROUP_CLAIM",
		"DURPDEPLOY_OIDC_REQUIRE_EMAIL_VERIFIED",
	} {
		key := envKey
		value, set := os.LookupEnv(key)
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset %s: %v", key, err)
		}
		t.Cleanup(func() {
			var err error
			if set {
				err = os.Setenv(key, value)
			} else {
				err = os.Unsetenv(key)
			}
			if err != nil {
				t.Errorf("restore %s: %v", key, err)
			}
		})
	}
}

func equalConfig(got, want Config) bool {
	return got.Enabled == want.Enabled &&
		got.Issuer == want.Issuer &&
		got.ClientID == want.ClientID &&
		got.ClientSecret == want.ClientSecret &&
		got.CallbackURL == want.CallbackURL &&
		strings.Join(got.Scopes, " ") == strings.Join(want.Scopes, " ") &&
		got.DisplayName == want.DisplayName &&
		got.GroupClaim == want.GroupClaim &&
		got.AdminGroup == want.AdminGroup &&
		got.DeployerGroup == want.DeployerGroup &&
		got.ViewerGroup == want.ViewerGroup &&
		got.AllowUnverifiedEmail == want.AllowUnverifiedEmail
}
