package config

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func lookup(values map[string]string) Lookup {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

func TestLoadDefaultsAndPresence(t *testing.T) {
	cfg, err := LoadWithLookup(lookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Limits, Defaults().Limits) ||
		cfg.Addr != ":8080" || cfg.Container.Enabled != "true" {
		t.Fatalf("unexpected defaults: %+v", cfg.Limits)
	}
	if _, set := cfg.LookupEnv("DURPDEPLOY_OIDC_DISPLAY_NAME"); set {
		t.Fatal("default must not enable OIDC")
	}
	if cfg.Limits.Execution.MaxRetries != 10 ||
		cfg.Limits.Staging.ArchiveBytes != 544<<20 ||
		cfg.Limits.Transfer.Global != 4 {
		t.Fatal("historical resource defaults changed")
	}
}

func TestLoadFileEnvironmentPrecedenceAndIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	const source = `addr: "127.0.0.1:1234"
smtp:
  host: "mail.example.test"
  pass: "file-password"
oidc:
  display_name: "File SSO"
  require_email_verified: false
limits:
  execution:
    max_retries: 20
  transfer:
    global: 8
    per_agent: 2
`
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		FileEnv:                                   path,
		"DURPDEPLOY_ADDR":                         "127.0.0.1:5678",
		"DURPDEPLOY_SMTP_PASS":                    "",
		"DURPDEPLOY_OIDC_DISPLAY_NAME":            "",
		"DURPDEPLOY_LIMITS_EXECUTION_MAX_RETRIES": "0",
	}
	cfg, err := LoadWithLookup(lookup(env))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != env["DURPDEPLOY_ADDR"] ||
		cfg.SMTP.Host != "mail.example.test" || cfg.SMTP.Pass != "" ||
		cfg.OIDC.DisplayName != "" || cfg.OIDC.RequireEmailVerified != "false" ||
		cfg.Limits.Execution.MaxRetries != 0 || cfg.Limits.Transfer.Global != 8 ||
		cfg.Limits.Artifact.DownloadBytes != 300<<20 {
		t.Fatal("configuration precedence or partial defaults are incorrect")
	}
	for _, key := range []string{
		"DURPDEPLOY_SMTP_PASS", "DURPDEPLOY_OIDC_DISPLAY_NAME",
	} {
		if value, set := cfg.LookupEnv(key); !set || value != "" {
			t.Fatalf("explicit empty presence lost for %s", key)
		}
	}
	delete(env, "DURPDEPLOY_SMTP_PASS")
	if value, set := cfg.LookupEnv("DURPDEPLOY_SMTP_PASS"); !set ||
		value != "" {
		t.Fatal("configuration changed after environment mutation")
	}
	other, err := LoadWithLookup(lookup(nil))
	if err != nil || other.Limits.Transfer.Global != 4 ||
		other.SMTP.Host != "" {
		t.Fatal("configuration leaked between instances")
	}
}

func TestEveryFieldSupportsYAMLAndEnvironment(t *testing.T) {
	base := Defaults()
	err := visitFields(reflect.ValueOf(base), "",
		func(v reflect.Value, path, env string) error {
			t.Run(path, func(t *testing.T) {
				exerciseFieldBinding(t, v, path, env)
			})
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
}

func exerciseFieldBinding(t *testing.T, value reflect.Value, path, env string) {
	t.Helper()
	var raw string
	switch value.Kind() {
	case reflect.String:
		raw = "explicit-value"
	case reflect.Int64:
		raw = strconv.FormatInt(value.Int()+1, 10)
	case reflect.Float64:
		raw = strconv.FormatFloat(value.Float()+0.5, 'g', -1, 64)
	}
	cfg, err := LoadWithLookup(lookup(map[string]string{env: raw}))
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := cfg.LookupEnv(env); !ok || got != raw {
		t.Fatalf("environment binding failed for %s", path)
	}
	assertTypedValue(t, cfg, path, raw)
	source := fieldYAML(path, raw, value.Kind() == reflect.String)
	cfg, err = resolve(strings.NewReader(source), lookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := cfg.LookupEnv(env); !ok || got != raw {
		t.Fatalf("YAML binding failed for %s", path)
	}
	assertTypedValue(t, cfg, path, raw)
}

func fieldYAML(path, raw string, quoted bool) string {
	parts := strings.Split(path, ".")
	var source strings.Builder
	last := len(parts) - 1
	for i, part := range parts[:last] {
		_, _ = fmt.Fprintf(&source, "%s%s:\n", strings.Repeat("  ", i), part)
	}
	if quoted {
		raw = strconv.Quote(raw)
	}
	_, _ = fmt.Fprintf(&source, "%s%s: %s\n",
		strings.Repeat("  ", last), parts[last], raw)
	return source.String()
}

func assertTypedValue(t *testing.T, cfg Config, path, want string) {
	t.Helper()
	value := reflect.ValueOf(cfg)
	for _, part := range strings.Split(path, ".") {
		index := fieldIndex(value.Type(), part)
		if index < 0 {
			t.Fatalf("missing typed field %s", path)
		}
		value = value.Field(index)
	}
	if got := fmt.Sprint(value.Interface()); got != want {
		t.Fatalf("typed field %s = %s, want %s", path, got, want)
	}
}

func TestMalformedConfigurationDoesNotExposeValues(t *testing.T) {
	const marker = "private-credential-marker"
	cases := []string{
		"smtp: [" + marker,
		"secret_key: " + marker + "\nsecret_key: duplicate\n",
		"smtp:\n  " + marker + ": true\n",
		"smtp:\n  pass: [" + marker + "]\n",
		"smtp:\n  pass: null\n",
		"smtp: null\n",
		"smtp:\n  pass: " + marker + "\n---\n{}\n",
		"limits:\n  transfer:\n    global: 1.5\n",
		"limits:\n  transfer:\n    global: '4'\n",
		"limits:\n  transfer:\n    global: 9223372036854775808\n",
		"limits:\n  container:\n    helper_cpus: .nan\n",
		"limits:\n  container:\n    helper_cpus: .inf\n",
		"smtp: &settings\n  pass: " + marker + "\noidc: *settings\n",
		"smtp:\n  <<: {pass: " + marker + "}\n",
		"[]\n", "", "---\n",
	}
	for i, source := range cases {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			_, err := resolve(strings.NewReader(source), lookup(nil))
			if err == nil {
				t.Fatal("expected invalid YAML or policy rejection")
			}
			if strings.Contains(err.Error(), marker) {
				t.Fatal("configuration error exposed a credential")
			}
		})
	}
	for _, raw := range []string{marker, "", "1.5", "9223372036854775808"} {
		_, err := LoadWithLookup(lookup(map[string]string{
			"DURPDEPLOY_LIMITS_TRANSFER_GLOBAL": raw,
		}))
		if err == nil || strings.Contains(err.Error(), marker) {
			t.Fatal("invalid environment value was accepted or exposed")
		}
	}
}

func TestFileSelection(t *testing.T) {
	_, err := LoadWithLookup(lookup(map[string]string{
		FileEnv: filepath.Join(t.TempDir(), "missing.yaml"),
	}))
	if err == nil {
		t.Fatal("selected missing file must fail")
	}
	if _, err := LoadWithLookup(lookup(map[string]string{FileEnv: ""})); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DURPDEPLOY_LIMITS_EXECUTION_MAX_RETRIES", "7")
	t.Setenv(FileEnv, "")
	cfg, err := Load()
	if err != nil || cfg.Limits.Execution.MaxRetries != 7 {
		t.Fatal("process environment was not loaded")
	}
}

func TestLimitsRejectUnsafeCombinations(t *testing.T) {
	cases := map[string]func(*Limits){
		"negative retries": func(l *Limits) { l.Execution.MaxRetries = -1 },
		"zero duration":    func(l *Limits) { l.Transfer.IdleTimeoutSeconds = 0 },
		"duration overflow": func(l *Limits) {
			l.Execution.MaxTimeoutSeconds = math.MaxInt64/int64(time.Second) + 1
		},
		"default exceeds max":      func(l *Limits) { l.Execution.MaxTimeoutSeconds = 1 },
		"per-agent exceeds global": func(l *Limits) { l.Transfer.PerAgent = 5 },
		"artifact nodes":           func(l *Limits) { l.Artifact.StagingNodes = 1 },
		"archive framing": func(l *Limits) {
			l.Staging.ArchiveBytes = l.Staging.PayloadBytes
		},
		"form limits":            func(l *Limits) { l.HTTP.FormBytes = 1 },
		"repository form limits": func(l *Limits) { l.HTTP.RepositoryFormBytes = l.HTTP.FormBytes + 1 },
		"header deadline":        func(l *Limits) { l.HTTP.ReadTimeoutSeconds = 1 },
		"log line":               func(l *Limits) { l.Logs.LineBytes = 1 },
		"log page":               func(l *Limits) { l.Logs.PageBytes = 1024 },
		"escaped log row":        func(l *Limits) { l.Logs.LineBytes = l.Logs.PageBytes },
		"CPU zero":               func(l *Limits) { l.Container.HelperCPUs = 0 },
		"CPU overflow":           func(l *Limits) { l.Container.HelperCPUs = math.MaxFloat64 },
		"CPU nan":                func(l *Limits) { l.Container.HelperCPUs = math.NaN() },
		"CPU inf":                func(l *Limits) { l.Container.HelperCPUs = math.Inf(1) },
		"artifact tmpfs overflow": func(l *Limits) {
			l.Artifact.DownloadBytes = math.MaxInt64 - 1
		},
		"staging tmpfs overflow": func(l *Limits) { l.Staging.Entries = math.MaxInt64 - 1 },
		"helper memory overflow": func(l *Limits) {
			l.Container.HelperMemoryOverheadBytes = math.MaxInt64 - 1
		},
		"sentinel overflow": func(l *Limits) { l.HTTP.JSONBytes = math.MaxInt64 },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			limits := Defaults().Limits
			change(&limits)
			if err := limits.Validate(); err == nil {
				t.Fatal("unsafe configuration accepted")
			}
		})
	}
}

func TestMergedPolicyValidationAndExample(t *testing.T) {
	const source = "limits:\n  transfer:\n    global: 0\n"
	cfg, err := resolve(strings.NewReader(source), lookup(map[string]string{
		"DURPDEPLOY_LIMITS_TRANSFER_GLOBAL": "6",
	}))
	if err != nil || cfg.Limits.Transfer.Global != 6 {
		t.Fatal("policy must be validated after environment overrides")
	}
	data, err := os.ReadFile("../../durpdeploy.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err = resolve(strings.NewReader(string(data)), lookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Limits, Defaults().Limits) ||
		cfg.SMTP.Host != "" || cfg.OIDC.Issuer != "" {
		t.Fatal("complete example changed the default resource policy")
	}
	limits := Defaults().Limits
	limits.Execution.MaxRetries = 20
	limits.Execution.MaxTimeoutSeconds = 172800
	limits.Container.HelperCPUs = 0.5
	limits.Transfer.Global = 8
	limits.Transfer.PerAgent = 2
	if err := limits.Validate(); err != nil {
		t.Fatalf("safe raised or fractional limits rejected: %v", err)
	}
}
