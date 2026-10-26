// Package config resolves startup settings from environment, YAML, and defaults.
// It does not modify the process environment or perform startup side effects.
package config

const DefaultDSN = "durpdeploy.db?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
const DefaultAuditRetentionDays = 180

// Config contains server startup settings. LookupEnv preserves explicit presence
// for legacy validators that distinguish an empty setting from an absent one.
type Config struct {
	Addr               string        `yaml:"addr"                 env:"DURPDEPLOY_ADDR"`
	DB                 string        `yaml:"db"                   env:"DURPDEPLOY_DB"`
	URL                string        `yaml:"url"                  env:"DURPDEPLOY_URL"`
	SecretKey          string        `yaml:"secret_key"           env:"DURPDEPLOY_SECRET_KEY"`
	TrustedProxies     string        `yaml:"trusted_proxies"      env:"DURPDEPLOY_TRUSTED_PROXIES"`
	AuditRetentionDays string        `yaml:"audit_retention_days" env:"DURPDEPLOY_AUDIT_RETENTION_DAYS"`
	ExtraScrubPatterns string        `yaml:"extra_scrub_patterns" env:"DURPDEPLOY_EXTRA_SCRUB_PATTERNS"`
	ExecutionBoundary  string        `yaml:"execution_boundary"   env:"DURPDEPLOY_EXECUTION_BOUNDARY"`
	OIDC               OIDC          `yaml:"oidc"`
	SMTP               SMTP          `yaml:"smtp"`
	Container          Container     `yaml:"container"`
	AgentListener      AgentListener `yaml:"agent_listener"`
	Litestream         Litestream    `yaml:"litestream"`
	Limits             Limits        `yaml:"limits"`

	supplied map[string]string
}

type OIDC struct {
	Issuer               string `yaml:"issuer"                 env:"DURPDEPLOY_OIDC_ISSUER"`
	ClientID             string `yaml:"client_id"              env:"DURPDEPLOY_OIDC_CLIENT_ID"`
	ClientSecret         string `yaml:"client_secret"          env:"DURPDEPLOY_OIDC_CLIENT_SECRET"`
	AdminGroup           string `yaml:"admin_group"            env:"DURPDEPLOY_OIDC_ADMIN_GROUP"`
	MaintainerGroup      string `yaml:"maintainer_group"       env:"DURPDEPLOY_OIDC_MAINTAINER_GROUP"`
	DeployerGroup        string `yaml:"deployer_group"         env:"DURPDEPLOY_OIDC_DEPLOYER_GROUP"`
	ViewerGroup          string `yaml:"viewer_group"           env:"DURPDEPLOY_OIDC_VIEWER_GROUP"`
	DisplayName          string `yaml:"display_name"           env:"DURPDEPLOY_OIDC_DISPLAY_NAME"`
	GroupClaim           string `yaml:"group_claim"            env:"DURPDEPLOY_OIDC_GROUP_CLAIM"`
	RequireEmailVerified string `yaml:"require_email_verified" env:"DURPDEPLOY_OIDC_REQUIRE_EMAIL_VERIFIED"`
}

type SMTP struct {
	Host string `yaml:"host" env:"DURPDEPLOY_SMTP_HOST"`
	Port string `yaml:"port" env:"DURPDEPLOY_SMTP_PORT"`
	From string `yaml:"from" env:"DURPDEPLOY_SMTP_FROM"`
	User string `yaml:"user" env:"DURPDEPLOY_SMTP_USER"`
	Pass string `yaml:"pass" env:"DURPDEPLOY_SMTP_PASS"`
}

type Container struct {
	Enabled   string `yaml:"enabled"   env:"DURPDEPLOY_EMBEDDED_AGENT_ENABLED"`
	Runtime   string `yaml:"runtime"   env:"DURPDEPLOY_CONTAINER_RUNTIME"`
	URL       string `yaml:"url"       env:"DURPDEPLOY_CONTAINER_URL"`
	Namespace string `yaml:"namespace" env:"DURPDEPLOY_CONTAINER_NAMESPACE"`
}

type AgentListener struct {
	ListenAddr  string `yaml:"listen_addr"  env:"DURPDEPLOY_AGENT_LISTEN_ADDR"`
	PublicURL   string `yaml:"public_url"   env:"DURPDEPLOY_AGENT_PUBLIC_URL"`
	IdentityDir string `yaml:"identity_dir" env:"DURPDEPLOY_AGENT_IDENTITY_DIR"`
}

type Litestream struct {
	CheckCommand  string `yaml:"check_command"  env:"DURPDEPLOY_LITESTREAM_CHECK_COMMAND"`
	CheckInterval string `yaml:"check_interval" env:"DURPDEPLOY_LITESTREAM_CHECK_INTERVAL"`
}

// LookupEnv returns only explicitly supplied values, including empty strings.
// It never falls back to the live environment or returns a built-in default.
func (c Config) LookupEnv(key string) (string, bool) {
	value, ok := c.supplied[key]
	return value, ok
}
