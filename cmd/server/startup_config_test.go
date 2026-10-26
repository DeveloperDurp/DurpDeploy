package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"durpdeploy/internal/config"
	"durpdeploy/internal/notify"
)

func startupLookup(values map[string]string) config.Lookup {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

func TestStartupConfigUsesYAMLAndEnvironmentSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "startup.yaml")
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	source := "url: 'https://yaml.example.test'\nsecret_key: '" + key + "'\n" +
		"container:\n  enabled: false\n" +
		"limits:\n  token_telemetry:\n    concurrency: 2\n    interval_seconds: 120\n    write_timeout_seconds: 1\n" +
		"  http:\n    read_header_timeout_seconds: 1\n    read_timeout_seconds: 2\n" +
		"    write_timeout_seconds: 3\n    idle_timeout_seconds: 4\n    stream_write_timeout_seconds: 1\n" +
		"smtp:\n  host: 'mail.example.test'\n  port: 587\n" +
		"oidc:\n  issuer: 'https://issuer.example.test/realm'\n" +
		"  client_id: 'yaml-client'\n  client_secret: 'private-test-value'\n" +
		"  admin_group: 'admins'\n  deployer_group: 'deployers'\n  viewer_group: 'viewers'\n"
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{
		config.FileEnv:   path,
		"DURPDEPLOY_URL": "https://env.example.test",
		"DURPDEPLOY_LIMITS_TOKEN_TELEMETRY_INTERVAL_SECONDS": "10",
		"DURPDEPLOY_LIMITS_HTTP_WRITE_TIMEOUT_SECONDS":       "120",
	}
	startup, err := loadServerStartupWithLookup(startupLookup(values))
	if err != nil {
		t.Fatal(err)
	}
	if startup.settings.Limits.TokenTelemetry != (config.TokenTelemetryLimits{
		Concurrency: 2, IntervalSeconds: 10, WriteTimeoutSeconds: 1,
	}) {
		t.Fatal("startup did not resolve the telemetry policy")
	}
	limits := startup.settings.Limits.HTTP
	if limits.ReadHeaderTimeoutSeconds != 1 || limits.ReadTimeoutSeconds != 2 ||
		limits.WriteTimeoutSeconds != 120 || limits.IdleTimeoutSeconds != 4 ||
		limits.StreamWriteTimeoutSeconds != 1 {
		t.Fatal("startup did not resolve HTTP timeouts")
	}
	if !startup.mfa.CookieSecure ||
		startup.mfa.WebAuthn.Origin != "https://env.example.test" ||
		startup.oidc.CallbackURL != "https://env.example.test/login/oidc/callback" ||
		startup.oidc.ClientID != "yaml-client" {
		t.Fatal(
			"authentication services did not use the merged startup settings",
		)
	}
	mail := notify.LoadSMTPConfig(startup.settings.LookupEnv)
	if mail.Host != "mail.example.test" || mail.Port != "587" {
		t.Fatal("SMTP did not use YAML settings")
	}
	values["DURPDEPLOY_URL"] = "https://changed.example.test"
	if value, _ := startup.settings.LookupEnv("DURPDEPLOY_URL"); value != "https://env.example.test" {
		t.Fatal("startup snapshot changed with its input map")
	}
	values["DURPDEPLOY_OIDC_DISPLAY_NAME"] = ""
	if _, err := loadServerStartupWithLookup(startupLookup(values)); err == nil {
		t.Fatal("explicit empty OIDC value must retain its validation")
	}
}

func TestStartupConfigRejectsInvalidSettingsBeforeEffects(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	for _, test := range []struct{ name, key, value string }{
		{"address", "DURPDEPLOY_ADDR", "invalid"},
		{"port", "DURPDEPLOY_ADDR", "127.0.0.1:99999"},
		{"database", "DURPDEPLOY_DB", "postgres://user:private-value@host/?sslmode=invalid"},
		{"origin", "DURPDEPLOY_URL", "http://external.example.test"},
		{"trusted proxies", "DURPDEPLOY_TRUSTED_PROXIES", "private-value"},
		{"runtime", "DURPDEPLOY_CONTAINER_RUNTIME", "invalid"},
		{"runtime toggle", "DURPDEPLOY_EMBEDDED_AGENT_ENABLED", "invalid"},
		{"runtime socket", "DURPDEPLOY_CONTAINER_URL", "tcp://runtime.example.test:2375"},
		{"scrub pattern", "DURPDEPLOY_EXTRA_SCRUB_PATTERNS", "[private-value"},
		{"backup interval", "DURPDEPLOY_LITESTREAM_CHECK_INTERVAL", "private-value"},
		{"secret key", "DURPDEPLOY_SECRET_KEY", "private-value"},
		{"zero log batch", "DURPDEPLOY_LIMITS_LOGS_PREVIEW_BATCH", "0"},
		{"archive framing", "DURPDEPLOY_LIMITS_STAGING_ARCHIVE_BYTES", "536870912"},
		{"zero global transfers", "DURPDEPLOY_LIMITS_TRANSFER_GLOBAL", "0"},
		{"per-agent exceeds global", "DURPDEPLOY_LIMITS_TRANSFER_PER_AGENT", "5"},
		{"zero transfer idle timeout", "DURPDEPLOY_LIMITS_TRANSFER_IDLE_TIMEOUT_SECONDS", "0"},
		{"zero workload PIDs", "DURPDEPLOY_LIMITS_CONTAINER_PIDS", "0"},
		{"unlimited helper PIDs", "DURPDEPLOY_LIMITS_CONTAINER_HELPER_PIDS", "-1"},
		{"zero tmpfs policy", "DURPDEPLOY_LIMITS_CONTAINER_TMPFS_BYTES", "0"},
		{"zero helper CPUs", "DURPDEPLOY_LIMITS_CONTAINER_HELPER_CPUS", "0"},
		{"zero helper memory", "DURPDEPLOY_LIMITS_CONTAINER_HELPER_MEMORY_OVERHEAD_BYTES", "0"},
		{"HTTP zero JSON body", "DURPDEPLOY_LIMITS_HTTP_JSON_BYTES", "0"},
		{"HTTP zero password body", "DURPDEPLOY_LIMITS_HTTP_PASSWORD_FORM_BYTES", "0"},
		{"HTTP zero repository body", "DURPDEPLOY_LIMITS_HTTP_REPOSITORY_FORM_BYTES", "0"},
		{"HTTP zero header timeout", "DURPDEPLOY_LIMITS_HTTP_READ_HEADER_TIMEOUT_SECONDS", "0"},
		{"HTTP invalid header timeout", "DURPDEPLOY_LIMITS_HTTP_READ_HEADER_TIMEOUT_SECONDS", "31"},
		{"HTTP duration overflow", "DURPDEPLOY_LIMITS_HTTP_WRITE_TIMEOUT_SECONDS", "9223372037"},
		{"telemetry capacity", "DURPDEPLOY_LIMITS_TOKEN_TELEMETRY_CONCURRENCY", "0"},
		{"telemetry interval", "DURPDEPLOY_LIMITS_TOKEN_TELEMETRY_INTERVAL_SECONDS", "0"},
		{"telemetry deadline", "DURPDEPLOY_LIMITS_TOKEN_TELEMETRY_WRITE_TIMEOUT_SECONDS", "0"},
		{"telemetry duration overflow", "DURPDEPLOY_LIMITS_TOKEN_TELEMETRY_WRITE_TIMEOUT_SECONDS", "9223372037"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			values := map[string]string{
				"DURPDEPLOY_DB": filepath.Join(
					dir,
					"must-not-exist.db",
				),
				"DURPDEPLOY_SECRET_KEY":               key,
				"DURPDEPLOY_LITESTREAM_CHECK_COMMAND": "true",
				"DURPDEPLOY_AGENT_IDENTITY_DIR": filepath.Join(
					dir,
					"identity",
				),
				test.key: test.value,
			}
			_, err := loadServerStartupWithLookup(startupLookup(values))
			if err == nil || strings.Contains(err.Error(), "private-value") {
				t.Fatal("invalid configuration was accepted or exposed a value")
			}
			entries, readErr := os.ReadDir(dir)
			if readErr != nil || len(entries) != 0 {
				t.Fatal("startup preflight created files")
			}
		})
	}
}

func TestStartupContainerPIDPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "startup.yaml")
	if err := os.WriteFile(path, []byte("limits:\n  container:\n    pids: 16\n    helper_pids: 8\n    tmpfs_bytes: 1048576\n    helper_cpus: 0.5\n    helper_memory_overhead_bytes: 8388608\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	startup, err := loadServerStartupWithLookup(startupLookup(map[string]string{
		config.FileEnv: path,
		"DURPDEPLOY_SECRET_KEY": base64.StdEncoding.EncodeToString(
			make([]byte, 32),
		),
		"DURPDEPLOY_LIMITS_CONTAINER_PIDS":        "160",
		"DURPDEPLOY_LIMITS_CONTAINER_TMPFS_BYTES": "134217728",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if startup.settings.Limits.Container.PIDs != 160 ||
		startup.settings.Limits.Container.HelperPIDs != 8 ||
		startup.settings.Limits.Container.TmpfsBytes != 134217728 ||
		startup.settings.Limits.Container.HelperCPUs != 0.5 ||
		startup.settings.Limits.Container.HelperMemoryOverheadBytes != 8388608 {
		t.Fatal("startup did not capture merged container limits")
	}
}

func TestStartupStagingPayloadPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "startup.yaml")
	if err := os.WriteFile(path, []byte("limits:\n  staging:\n    payload_bytes: 1024\n    entries: 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{
		config.FileEnv: path,
		"DURPDEPLOY_SECRET_KEY": base64.StdEncoding.EncodeToString(
			make([]byte, 32),
		),
		"DURPDEPLOY_LIMITS_STAGING_PAYLOAD_BYTES": "536870913",
		"DURPDEPLOY_LIMITS_STAGING_ENTRIES":       "20001",
	}
	startup, err := loadServerStartupWithLookup(startupLookup(values))
	if err != nil {
		t.Fatal(err)
	}
	if startup.settings.Limits.Staging != (config.StagingLimits{
		PayloadBytes: 536870913, Entries: 20001, ArchiveBytes: 570425344,
	}) {
		t.Fatal("startup did not capture merged staging limits")
	}
	delete(values, "DURPDEPLOY_LIMITS_STAGING_PAYLOAD_BYTES")
	delete(values, "DURPDEPLOY_LIMITS_STAGING_ENTRIES")
	startup, err = loadServerStartupWithLookup(startupLookup(values))
	if err != nil || startup.settings.Limits.Staging.PayloadBytes != 1024 ||
		startup.settings.Limits.Staging.Entries != 2 {
		t.Fatalf("YAML staging limits were not applied: %v", err)
	}
}

func TestStartupHTTPBodyPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "startup.yaml")
	if err := os.WriteFile(path, []byte("limits:\n  http:\n    json_bytes: 1024\n    form_bytes: 4096\n    password_form_bytes: 512\n    repository_form_bytes: 768\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	startup, err := loadServerStartupWithLookup(startupLookup(map[string]string{
		config.FileEnv: path,
		"DURPDEPLOY_SECRET_KEY": base64.StdEncoding.EncodeToString(
			make([]byte, 32),
		),
		"DURPDEPLOY_LIMITS_HTTP_JSON_BYTES": "2048",
	}))
	if err != nil {
		t.Fatal(err)
	}
	limits := startup.settings.Limits.HTTP
	if limits.JSONBytes != 2048 || limits.FormBytes != 4096 ||
		limits.PasswordFormBytes != 512 || limits.RepositoryFormBytes != 768 {
		t.Fatal("startup did not capture the merged HTTP body limits")
	}
}
