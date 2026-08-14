package mfa

import (
	"testing"

	"durpdeploy/internal/secret"
)

func TestNewService_retainsConfigAndSecretBox(t *testing.T) {
	// Given: validated MFA configuration and the existing encryption box.
	config := Config{
		WebAuthn: WebAuthnConfig{
			Enabled: true,
			Origin:  "https://deploy.example.com",
			RPID:    "deploy.example.com",
		},
		CookieSecure: true,
	}
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatalf("secret.NewBox(): %v", err)
	}

	// When: startup dependencies are assembled into the concrete service.
	service := NewService(config, box)

	// Then: both validated dependencies are retained unchanged.
	if service.config != config {
		t.Fatalf("service config = %#v, want %#v", service.config, config)
	}
	if service.box != box {
		t.Fatal("service did not retain the supplied secret box")
	}
}
