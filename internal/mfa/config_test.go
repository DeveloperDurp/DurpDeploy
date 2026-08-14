package mfa

import "testing"

func TestWebAuthnConfig(t *testing.T) {
	tests := []struct {
		name       string
		url        string
		wantOrigin string
		wantRPID   string
		wantSecure bool
		wantEnable bool
		wantErr    bool
	}{
		{
			name:       "unset disables passkeys",
			wantSecure: false,
		},
		{
			name:       "https origin",
			url:        "https://deploy.example.com",
			wantOrigin: "https://deploy.example.com",
			wantRPID:   "deploy.example.com",
			wantSecure: true,
			wantEnable: true,
		},
		{
			name:       "localhost http origin",
			url:        "http://localhost:8080",
			wantOrigin: "http://localhost:8080",
			wantRPID:   "localhost",
			wantEnable: true,
		},
		{
			name:       "loopback http origin",
			url:        "http://127.0.0.1:8080",
			wantOrigin: "http://127.0.0.1:8080",
			wantRPID:   "127.0.0.1",
			wantEnable: true,
		},
		{"malformed escape", "https://%zz", "", "", false, false, true},
		{
			"userinfo",
			"https://user:password@deploy.example.com",
			"",
			"",
			false,
			false,
			true,
		},
		{"path", "https://deploy.example.com/mfa", "", "", false, false, true},
		{
			"query",
			"https://deploy.example.com?next=login",
			"",
			"",
			false,
			false,
			true,
		},
		{
			"fragment",
			"https://deploy.example.com#login",
			"",
			"",
			false,
			false,
			true,
		},
		{"non-absolute", "deploy.example.com", "", "", false, false, true},
		{
			"wrong scheme",
			"ftp://deploy.example.com",
			"",
			"",
			false,
			false,
			true,
		},
		{
			"public http",
			"http://deploy.example.com",
			"",
			"",
			false,
			false,
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given: a configured public URL.
			t.Setenv("DURPDEPLOY_URL", tt.url)
			// When: startup loads its typed MFA configuration.
			config, err := LoadConfig()
			// Then: accepted origins are exact and unsafe values fail closed.
			if tt.wantErr {
				if err == nil {
					t.Fatal("LoadConfig() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadConfig() error = %v", err)
			}
			if config.WebAuthn.Enabled != tt.wantEnable {
				t.Errorf(
					"WebAuthn.Enabled = %t, want %t",
					config.WebAuthn.Enabled,
					tt.wantEnable,
				)
			}
			if config.WebAuthn.Origin != tt.wantOrigin {
				t.Errorf(
					"WebAuthn.Origin = %q, want %q",
					config.WebAuthn.Origin,
					tt.wantOrigin,
				)
			}
			if config.WebAuthn.RPID != tt.wantRPID {
				t.Errorf(
					"WebAuthn.RPID = %q, want %q",
					config.WebAuthn.RPID,
					tt.wantRPID,
				)
			}
			if config.CookieSecure != tt.wantSecure {
				t.Errorf(
					"CookieSecure = %t, want %t",
					config.CookieSecure,
					tt.wantSecure,
				)
			}
		})
	}
}

func TestCookiePolicy(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want bool
	}{
		{"unset", "", false},
		{"https", "https://deploy.example.com", true},
		{"localhost http", "http://localhost:8080", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given: a valid URL configuration.
			t.Setenv("DURPDEPLOY_URL", tt.url)
			// When: its cookie policy is loaded.
			config, err := LoadConfig()
			// Then: only HTTPS enables the Secure attribute.
			if err != nil {
				t.Fatalf("LoadConfig() error = %v", err)
			}
			if config.CookieSecure != tt.want {
				t.Errorf(
					"CookieSecure = %t, want %t",
					config.CookieSecure,
					tt.want,
				)
			}
		})
	}
}
