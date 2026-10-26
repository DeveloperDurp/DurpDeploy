package config

import "strconv"

const (
	DefaultJSONBodyBytes           int64 = 4 << 20
	DefaultFormBodyBytes           int64 = 16 << 20
	DefaultPasswordFormBodyBytes   int64 = 64 << 10
	DefaultRepositoryFormBodyBytes int64 = 64 << 10
)

// Limits is a value snapshot. Consumers must not read environment variables.
// Numeric suffixes specify units; byte counts use bytes, not megabytes.
type Limits struct {
	Execution      ExecutionLimits      `yaml:"execution"       json:"execution"`
	Artifact       ArtifactLimits       `yaml:"artifact"        json:"artifact"`
	Staging        StagingLimits        `yaml:"staging"         json:"staging"`
	Merge          MergeLimits          `yaml:"merge"           json:"merge"`
	Transfer       TransferLimits       `yaml:"transfer"        json:"transfer"`
	Container      ContainerLimits      `yaml:"container"       json:"container"`
	HTTP           HTTPLimits           `yaml:"http"            json:"http"`
	Logs           LogLimits            `yaml:"logs"            json:"logs"`
	TokenTelemetry TokenTelemetryLimits `yaml:"token_telemetry" json:"token_telemetry"`
}

// Resource environment names are derived from the YAML path below limits.
// For example: DURPDEPLOY_LIMITS_EXECUTION_MAX_RETRIES.
type ExecutionLimits struct {
	MaxRetries            int64 `yaml:"max_retries"             json:"max_retries"`
	MaxTimeoutSeconds     int64 `yaml:"max_timeout_seconds"     json:"max_timeout_seconds"`
	DefaultTimeoutSeconds int64 `yaml:"default_timeout_seconds" json:"default_timeout_seconds"`
}

type ArtifactLimits struct {
	DownloadBytes        int64 `yaml:"download_bytes"         json:"download_bytes"`
	ExtractedBytes       int64 `yaml:"extracted_bytes"        json:"extracted_bytes"`
	Files                int64 `yaml:"files"                  json:"files"`
	StagingNodes         int64 `yaml:"staging_nodes"          json:"staging_nodes"`
	MetadataBytes        int64 `yaml:"metadata_bytes"         json:"metadata_bytes"`
	FetchTimeoutSeconds  int64 `yaml:"fetch_timeout_seconds"  json:"fetch_timeout_seconds"`
	DialTimeoutSeconds   int64 `yaml:"dial_timeout_seconds"   json:"dial_timeout_seconds"`
	TLSTimeoutSeconds    int64 `yaml:"tls_timeout_seconds"    json:"tls_timeout_seconds"`
	HeaderTimeoutSeconds int64 `yaml:"header_timeout_seconds" json:"header_timeout_seconds"`
}

type StagingLimits struct {
	PayloadBytes int64 `yaml:"payload_bytes" json:"payload_bytes"`
	Entries      int64 `yaml:"entries"       json:"entries"`
	ArchiveBytes int64 `yaml:"archive_bytes" json:"archive_bytes"`
}

type MergeLimits struct {
	ArchiveBytes int64 `yaml:"archive_bytes" json:"archive_bytes"`
	EntryVisits  int64 `yaml:"entry_visits"  json:"entry_visits"`
	AgentOutputs int64 `yaml:"agent_outputs" json:"agent_outputs"`
}

type TransferLimits struct {
	Global             int64 `yaml:"global"               json:"global"`
	PerAgent           int64 `yaml:"per_agent"            json:"per_agent"`
	IdleTimeoutSeconds int64 `yaml:"idle_timeout_seconds" json:"idle_timeout_seconds"`
}

type ContainerLimits struct {
	PIDs                      int64   `yaml:"pids"                         json:"pids"`
	TmpfsBytes                int64   `yaml:"tmpfs_bytes"                  json:"tmpfs_bytes"`
	HelperPIDs                int64   `yaml:"helper_pids"                  json:"helper_pids"`
	HelperCPUs                float64 `yaml:"helper_cpus"                  json:"helper_cpus"`
	HelperMemoryOverheadBytes int64   `yaml:"helper_memory_overhead_bytes" json:"helper_memory_overhead_bytes"`
}

type HTTPLimits struct {
	JSONBytes                 int64 `yaml:"json_bytes"                   json:"json_bytes"`
	FormBytes                 int64 `yaml:"form_bytes"                   json:"form_bytes"`
	PasswordFormBytes         int64 `yaml:"password_form_bytes"          json:"password_form_bytes"`
	RepositoryFormBytes       int64 `yaml:"repository_form_bytes"        json:"repository_form_bytes"`
	ReadHeaderTimeoutSeconds  int64 `yaml:"read_header_timeout_seconds"  json:"read_header_timeout_seconds"`
	ReadTimeoutSeconds        int64 `yaml:"read_timeout_seconds"         json:"read_timeout_seconds"`
	WriteTimeoutSeconds       int64 `yaml:"write_timeout_seconds"        json:"write_timeout_seconds"`
	IdleTimeoutSeconds        int64 `yaml:"idle_timeout_seconds"         json:"idle_timeout_seconds"`
	StreamWriteTimeoutSeconds int64 `yaml:"stream_write_timeout_seconds" json:"stream_write_timeout_seconds"`
}

type LogLimits struct {
	PageBytes    int64 `yaml:"page_bytes"    json:"page_bytes"`
	LineBytes    int64 `yaml:"line_bytes"    json:"line_bytes"`
	PreviewBatch int64 `yaml:"preview_batch" json:"preview_batch"`
	PendingBytes int64 `yaml:"pending_bytes" json:"pending_bytes"`
	ExportBatch  int64 `yaml:"export_batch"  json:"export_batch"`
	PageRows     int64 `yaml:"page_rows"     json:"page_rows"`
}

type TokenTelemetryLimits struct {
	Concurrency         int64 `yaml:"concurrency"           json:"concurrency"`
	IntervalSeconds     int64 `yaml:"interval_seconds"      json:"interval_seconds"`
	WriteTimeoutSeconds int64 `yaml:"write_timeout_seconds" json:"write_timeout_seconds"`
}

// Defaults preserves the historical server policy. It returns a fresh value.
func Defaults() Config {
	return Config{
		Addr:               ":8080",
		DB:                 DefaultDSN,
		AuditRetentionDays: strconv.Itoa(DefaultAuditRetentionDays),
		OIDC: OIDC{
			DisplayName: "SSO", GroupClaim: "groups",
			RequireEmailVerified: "true",
		},
		Container: Container{Enabled: "true", Namespace: "durpdeploy"},
		AgentListener: AgentListener{
			ListenAddr: "0.0.0.0:23187",
			PublicURL:  "https://localhost:23187", IdentityDir: ".agent-identity",
		},
		Litestream: Litestream{CheckInterval: "1h"},
		Limits: Limits{
			Execution: ExecutionLimits{
				MaxRetries: 10, MaxTimeoutSeconds: 86400,
				DefaultTimeoutSeconds: 300,
			},
			Artifact: ArtifactLimits{
				DownloadBytes: 300 << 20, ExtractedBytes: 512 << 20,
				Files: 10000, StagingNodes: 19999, MetadataBytes: 16 << 20,
				FetchTimeoutSeconds: 300, DialTimeoutSeconds: 30,
				TLSTimeoutSeconds: 10, HeaderTimeoutSeconds: 30,
			},
			Staging: StagingLimits{
				PayloadBytes: 512 << 20, Entries: 20000, ArchiveBytes: 544 << 20,
			},
			Merge: MergeLimits{
				ArchiveBytes: 2 << 30, EntryVisits: 200000, AgentOutputs: 64,
			},
			Transfer: TransferLimits{
				Global: 4, PerAgent: 1, IdleTimeoutSeconds: 300,
			},
			Container: ContainerLimits{
				PIDs: 128, TmpfsBytes: 64 << 20, HelperPIDs: 32,
				HelperCPUs: 1, HelperMemoryOverheadBytes: 128 << 20,
			},
			HTTP: HTTPLimits{
				JSONBytes: DefaultJSONBodyBytes, FormBytes: DefaultFormBodyBytes,
				PasswordFormBytes:        DefaultPasswordFormBodyBytes,
				RepositoryFormBytes:      DefaultRepositoryFormBodyBytes,
				ReadHeaderTimeoutSeconds: 5,
				ReadTimeoutSeconds:       30, WriteTimeoutSeconds: 60,
				IdleTimeoutSeconds: 60, StreamWriteTimeoutSeconds: 60,
			},
			Logs: LogLimits{
				PageBytes: 1 << 20, LineBytes: 16 << 10, PreviewBatch: 64,
				PendingBytes: 1 << 20, ExportBatch: 256, PageRows: 500,
			},
			TokenTelemetry: TokenTelemetryLimits{
				Concurrency: 8, IntervalSeconds: 60, WriteTimeoutSeconds: 5,
			},
		}}
}
