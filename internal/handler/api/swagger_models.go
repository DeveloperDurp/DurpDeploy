// allow: SIZE_OK — swagger model definitions for the generated OpenAPI spec.
package api

// --- Database-mirror models returned directly by API handlers ---

// Environment represents a deployment target.
// swagger:model Environment
//
//nolint:unused // Used by Swagger specification generation.
type swaggerEnvironment struct {
	ID               int64   `json:"id"`
	Name             string  `json:"name"`
	Description      *string `json:"description"`
	Tags             *string `json:"tags"`
	CreatedAt        int64   `json:"created_at"`
	VerificationType string  `json:"verification_type"`
	// Admin-only; omitted from responses for other roles.
	VerificationTarget         *string `json:"verification_target,omitempty"`
	VerificationTimeoutSeconds int64   `json:"verification_timeout_seconds"`
}

// Lifecycle represents a deployment promotion pipeline.
// swagger:model Lifecycle
//
//nolint:unused // Used by Swagger specification generation.
type swaggerLifecycle struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	CreatedAt   int64   `json:"created_at"`
}

// LifecycleStage is a single stage in a lifecycle.
// swagger:model LifecycleStage
//
//nolint:unused // Used by Swagger specification generation.
type swaggerLifecycleStage struct {
	ID               int64 `json:"id"`
	LifecycleID      int64 `json:"lifecycle_id"`
	EnvironmentID    int64 `json:"environment_id"`
	SortOrder        int64 `json:"sort_order"`
	RequiresApproval int64 `json:"requires_approval"`
}

// Step is an executable script step within a project.
// swagger:model Step
//
//nolint:unused // Used by Swagger specification generation.
type swaggerStep struct {
	ID                 int64    `json:"id"`
	ProjectID          int64    `json:"project_id"`
	Name               string   `json:"name"`
	ScriptBody         string   `json:"script_body"`
	Interpreter        string   `json:"interpreter"`
	SortOrder          int64    `json:"sort_order"`
	CreatedAt          int64    `json:"created_at"`
	TimeoutSeconds     int64    `json:"timeout_seconds"`
	MaxRetries         int64    `json:"max_retries"`
	ExecutionTarget    string   `json:"execution_target"`
	AgentExecutionMode string   `json:"agent_execution_mode"`
	AgentSelectors     []string `json:"agent_selectors"`
	// Container image required for local steps and agent container mode.
	ContainerImage       string `json:"container_image"`
	NetworkMode          string `json:"network_mode"`
	ApprovalArtifactPath string `json:"approval_artifact_path"`
	ApprovalReviewPath   string `json:"approval_review_path"`
	ApprovalReviewFormat string `json:"approval_review_format"`
	// Optional variable-name restriction. Empty passes all variables.
	VariableNames []string `json:"variable_names"`
}

// StepTemplate is a reusable step template.
// swagger:model StepTemplate
//
//nolint:unused // Used by Swagger specification generation.
type swaggerStepTemplate struct {
	ID                   int64    `json:"id"`
	Name                 string   `json:"name"`
	ScriptBody           string   `json:"script_body"`
	Interpreter          string   `json:"interpreter"`
	CreatedAt            int64    `json:"created_at"`
	ExecutionTarget      string   `json:"execution_target"`
	AgentExecutionMode   string   `json:"agent_execution_mode"`
	AgentSelectors       []string `json:"agent_selectors"`
	ContainerImage       string   `json:"container_image"`
	NetworkMode          string   `json:"network_mode"`
	ApprovalArtifactPath string   `json:"approval_artifact_path"`
	ApprovalReviewPath   string   `json:"approval_review_path"`
	ApprovalReviewFormat string   `json:"approval_review_format"`
	VariableNames        []string `json:"variable_names"`
}

// StepTemplateVersion is a historical version of a step template.
// swagger:model StepTemplateVersion
//
//nolint:unused // Used by Swagger specification generation.
type swaggerStepTemplateVersion struct {
	ID                   int64    `json:"id"`
	TemplateID           int64    `json:"template_id"`
	VersionNumber        int64    `json:"version_number"`
	Name                 string   `json:"name"`
	ScriptBody           string   `json:"script_body"`
	Interpreter          string   `json:"interpreter"`
	CreatedAt            int64    `json:"created_at"`
	ExecutionTarget      string   `json:"execution_target"`
	AgentExecutionMode   string   `json:"agent_execution_mode"`
	AgentSelectors       []string `json:"agent_selectors"`
	ContainerImage       string   `json:"container_image"`
	NetworkMode          string   `json:"network_mode"`
	ApprovalArtifactPath string   `json:"approval_artifact_path"`
	ApprovalReviewPath   string   `json:"approval_review_path"`
	ApprovalReviewFormat string   `json:"approval_review_format"`
	VariableNames        []string `json:"variable_names"`
}

// Release is a refreshable snapshot of project steps and variables.
// swagger:model Release
//
//nolint:unused // Used by Swagger specification generation.
type swaggerRelease struct {
	ID        int64  `json:"id"`
	ProjectID int64  `json:"project_id"`
	Version   string `json:"version"`
	StepsJSON string `json:"steps_json"`
	CreatedAt int64  `json:"created_at"`
	// Deprecated: retained for compatibility; does not prevent refresh.
	SnapshotLocked int64 `json:"snapshot_locked"`
}

// Deployment represents a release executing against an environment.
// swagger:model Deployment
//
//nolint:unused // Used by Swagger specification generation.
type swaggerDeployment struct {
	QueuePosition      int64   `json:"queue_position"`
	ActiveDeploymentID int64   `json:"active_deployment_id,omitempty"`
	ActiveWorkURL      string  `json:"active_work_url,omitempty"`
	ID                 int64   `json:"id"`
	ReleaseID          int64   `json:"release_id"`
	EnvironmentID      int64   `json:"environment_id"`
	Status             string  `json:"status"`
	StartedAt          *int64  `json:"started_at"`
	FinishedAt         *int64  `json:"finished_at"`
	CreatedAt          int64   `json:"created_at"`
	Forced             int64   `json:"forced"`
	Note               *string `json:"note"`
}

// DeploymentListItem is the enriched row returned by ListDeployments.
// swagger:model DeploymentListItem
//
//nolint:unused // Used by Swagger specification generation.
type swaggerDeploymentListItem struct {
	ID              int64   `json:"id"`
	ReleaseID       int64   `json:"release_id"`
	EnvironmentID   int64   `json:"environment_id"`
	Status          string  `json:"status"`
	StartedAt       *int64  `json:"started_at"`
	FinishedAt      *int64  `json:"finished_at"`
	CreatedAt       int64   `json:"created_at"`
	Forced          int64   `json:"forced"`
	Note            *string `json:"note"`
	ProjectName     string  `json:"project_name"`
	ReleaseVersion  string  `json:"release_version"`
	EnvironmentName string  `json:"environment_name"`
}

// DeploymentListResponse is the paginated envelope for ListDeployments.
// swagger:model DeploymentListResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerDeploymentListResponse struct {
	Items  []swaggerDeploymentListItem `json:"items"`
	Total  int64                       `json:"total"`
	Limit  int64                       `json:"limit"`
	Offset int64                       `json:"offset"`
}

// DeploymentStatusResponse is the status payload for GetDeploymentStatus.
// swagger:model DeploymentStatusResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerDeploymentStatusResponse struct {
	Status             string `json:"status"`
	WaitingForAgents   bool   `json:"waiting_for_agents"`
	QueuePosition      int64  `json:"queue_position"`
	ActiveDeploymentID int64  `json:"active_deployment_id,omitempty"`
	ActiveWorkURL      string `json:"active_work_url,omitempty"`
}

// ScheduledDeployment is a cron-driven deployment configuration.
// swagger:model ScheduledDeployment
//
//nolint:unused // Used by Swagger specification generation.
type swaggerScheduledDeployment struct {
	ID            int64   `json:"id"`
	ProjectID     int64   `json:"project_id"`
	ReleaseID     int64   `json:"release_id"`
	EnvironmentID int64   `json:"environment_id"`
	Cron          string  `json:"cron"`
	NextRunAt     int64   `json:"next_run_at"`
	Enabled       int64   `json:"enabled"`
	LastFiredAt   *int64  `json:"last_fired_at"`
	LastError     string  `json:"last_error"`
	Note          *string `json:"note"`
	CreatedAt     int64   `json:"created_at"`
	UpdatedAt     int64   `json:"updated_at"`
}

// AuditLog is a recorded state-changing action.
// swagger:model AuditLog
//
//nolint:unused // Used by Swagger specification generation.
type swaggerAuditLog struct {
	ID         int64   `json:"id"`
	UserID     *int64  `json:"user_id"`
	Action     string  `json:"action"`
	EntityType string  `json:"entity_type"`
	EntityID   *int64  `json:"entity_id"`
	Details    *string `json:"details"`
	CreatedAt  int64   `json:"created_at"`
}

// NotificationEvent is a fired notification record.
// swagger:model NotificationEvent
//
//nolint:unused // Used by Swagger specification generation.
type swaggerNotificationEvent struct {
	ID            int64  `json:"id"`
	EventType     string `json:"event_type"`
	DeploymentID  *int64 `json:"deployment_id"`
	ProjectID     *int64 `json:"project_id"`
	EnvironmentID *int64 `json:"environment_id"`
	Message       string `json:"message"`
	Results       string `json:"results"`
	CreatedAt     int64  `json:"created_at"`
}

// ProjectMember is a single user membership on a project.
// swagger:model ProjectMember
//
//nolint:unused // Used by Swagger specification generation.
type swaggerProjectMember struct {
	ProjectID int64  `json:"project_id"`
	UserID    int64  `json:"user_id"`
	Role      string `json:"role"`
	CreatedAt int64  `json:"created_at"`
}

// User is a user account.
// swagger:model User
//
//nolint:unused // Used by Swagger specification generation.
type swaggerUser struct {
	ID          int64  `json:"id"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	Role        string `json:"role"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
	LastLoginAt *int64 `json:"last_login_at"`
}

// GlobalNotification holds the global notification settings.
// swagger:model GlobalNotification
//
//nolint:unused // Used by Swagger specification generation.
type swaggerGlobalNotification struct {
	SlackWebhookURLConfigured   bool    `json:"slack_webhook_url_configured"`
	GotifyTokenConfigured       bool    `json:"gotify_token_configured"`
	DiscordWebhookURLConfigured bool    `json:"discord_webhook_url_configured"`
	ID                          int64   `json:"id"`
	SlackWebhookURL             *string `json:"slack_webhook_url"`
	NotifyEmails                *string `json:"notify_emails"`
	GotifyURL                   *string `json:"gotify_url"`
	GotifyToken                 *string `json:"gotify_token"`
	DiscordWebhookURL           *string `json:"discord_webhook_url"`
	UpdatedAt                   int64   `json:"updated_at"`
}

// Variable is a project or environment-scoped variable.
// swagger:model Variable
//
//nolint:unused // Used by Swagger specification generation.
type swaggerVariable struct {
	ID            int64   `json:"id"`
	ProjectID     int64   `json:"project_id"`
	Name          string  `json:"name"`
	Value         *string `json:"value"`
	EnvironmentID *int64  `json:"environment_id"`
	CreatedAt     int64   `json:"created_at"`
	Secret        int64   `json:"secret"`
}

// LogEntry is a single line of deployment log output.
// swagger:model LogEntry
//
//nolint:unused // Used by Swagger specification generation.
type swaggerLogEntry struct {
	TS   int64  `json:"ts"`
	Step string `json:"step"`
	Line string `json:"line"`
}

// LogStreamParams controls the log stream format.
// swagger:model LogStreamParams
//
//nolint:unused // Used by Swagger specification generation.
type swaggerLogStreamParams struct {
	// Format is sse or ndjson; deployment streams also support structured SSE.
	//
	// in: query
	Format string `json:"format"`
}

// AuditLogParams filters the audit log list.
// swagger:model AuditLogParams
//
//nolint:unused // Used by Swagger specification generation.
type swaggerAuditLogParams struct {
	// in: query
	Limit int64 `json:"limit"`
	// in: query
	UserID int64 `json:"user_id"`
	// in: query
	Action string `json:"action"`
	// in: query
	EntityType string `json:"entity_type"`
}

// TokenResponse is the token representation returned by list endpoints.
// swagger:model TokenResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerTokenResponse struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Prefix     string  `json:"prefix"`
	Scope      string  `json:"scope"`
	Role       string  `json:"role"`
	ProjectIDs []int64 `json:"project_ids"`
	UserID     int64   `json:"user_id,omitempty"`
	Email      string  `json:"email,omitempty"`
	UserName   string  `json:"user_name,omitempty"`
	LastUsedAt *int64  `json:"last_used_at,omitempty"`
	ExpiresAt  *int64  `json:"expires_at,omitempty"`
	CreatedAt  int64   `json:"created_at"`
	RevokedAt  *int64  `json:"revoked_at,omitempty"`
}

// CreateTokenResponse returns the plaintext token once.
// swagger:model CreateTokenResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerCreateTokenResponse struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Prefix     string  `json:"prefix"`
	CreatedAt  int64   `json:"created_at"`
	TokenID    string  `json:"token_id"`
	Scope      string  `json:"scope"`
	Role       string  `json:"role"`
	ProjectIDs []int64 `json:"project_ids"`
}

// CreateTokenRequest is the body for CreateToken.
// swagger:model CreateTokenRequest
//
//nolint:unused // Used by Swagger specification generation.
type swaggerCreateTokenRequest struct {
	Name string `json:"name"`
	// Omit grant fields for legacy global creation. Scoped selections must be nonempty.
	// Both global and scoped tokens support an explicit permission ceiling.
	Scope string `json:"scope"`
	// Viewer, deployer, maintainer, or admin. This ceiling cannot increase owner permissions.
	Role       string  `json:"role"`
	ProjectIDs []int64 `json:"project_ids"`
}

// TokenScopeResponse identifies a token's permission ceiling.
// swagger:model TokenScopeResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerTokenScopeResponse struct {
	Scope      string  `json:"scope"`
	Role       string  `json:"role"`
	ProjectIDs []int64 `json:"project_ids"`
}

// ProjectListResponse is a list of projects.
// swagger:model ProjectListResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerProjectListResponse []swaggerProjectResponse

// ProjectResponse is the JSON shape for a project.
// swagger:model ProjectResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerProjectResponse struct {
	SlackWebhookURLConfigured   bool   `json:"slack_webhook_url_configured"`
	GotifyTokenConfigured       bool   `json:"gotify_token_configured"`
	DiscordWebhookURLConfigured bool   `json:"discord_webhook_url_configured"`
	ID                          int64  `json:"id"`
	Name                        string `json:"name"`
	Description                 string `json:"description"`
	CreatedAt                   int64  `json:"created_at"`
	LifecycleID                 *int64 `json:"lifecycle_id"`
	SlackWebhookURL             string `json:"slack_webhook_url"`
	NotifyEmails                string `json:"notify_emails"`
	GotifyURL                   string `json:"gotify_url"`
	GotifyToken                 string `json:"gotify_token"`
	DiscordWebhookURL           string `json:"discord_webhook_url"`
}

// ProjectNotificationResponse is the notification settings for a project.
// swagger:model ProjectNotificationResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerProjectNotificationResponse struct {
	SlackWebhookURLConfigured   bool   `json:"slack_webhook_url_configured"`
	GotifyTokenConfigured       bool   `json:"gotify_token_configured"`
	DiscordWebhookURLConfigured bool   `json:"discord_webhook_url_configured"`
	ID                          int64  `json:"id"`
	Name                        string `json:"name"`
	Description                 string `json:"description"`
	SlackWebhookURL             string `json:"slack_webhook_url"`
	NotifyEmails                string `json:"notify_emails"`
	GotifyURL                   string `json:"gotify_url"`
	GotifyToken                 string `json:"gotify_token"`
	DiscordWebhookURL           string `json:"discord_webhook_url"`
	CreatedAt                   int64  `json:"created_at"`
}

// ProjectNotificationRequest updates a project's notification settings.
// swagger:model ProjectNotificationRequest
//
//nolint:unused // Used by Swagger specification generation.
type swaggerProjectNotificationRequest struct {
	ClearSlackWebhookURL   bool   `json:"clear_slack_webhook_url"`
	ClearGotifyToken       bool   `json:"clear_gotify_token"`
	ClearDiscordWebhookURL bool   `json:"clear_discord_webhook_url"`
	SlackWebhookURL        string `json:"slack_webhook_url"`
	NotifyEmails           string `json:"notify_emails"`
	GotifyURL              string `json:"gotify_url"`
	GotifyToken            string `json:"gotify_token"`
	DiscordWebhookURL      string `json:"discord_webhook_url"`
}

// ProjectRequest is the body for create/update project.
// swagger:model ProjectRequest
//
//nolint:unused // Used by Swagger specification generation.
type swaggerProjectRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	LifecycleID int64  `json:"lifecycle_id"`
}

// EnvironmentRequest is the body for create/update environment.
// swagger:model EnvironmentRequest
//
//nolint:unused // Used by Swagger specification generation.
type swaggerEnvironmentRequest struct {
	Name                       string `json:"name"`
	Description                string `json:"description"`
	Tags                       string `json:"tags"`
	VerificationType           string `json:"verification_type"`
	VerificationTarget         string `json:"verification_target"`
	VerificationTimeoutSeconds int64  `json:"verification_timeout_seconds"`
}

// LifecycleRequest is the body for create/update lifecycle.
// swagger:model LifecycleRequest
//
//nolint:unused // Used by Swagger specification generation.
type swaggerLifecycleRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// LifecycleResponse is the detailed lifecycle payload.
// swagger:model LifecycleResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerLifecycleResponse struct {
	Lifecycle swaggerLifecycle        `json:"lifecycle"`
	Stages    []swaggerLifecycleStage `json:"stages"`
}

// LifecycleStageRequest is the body for add/update lifecycle stage.
// swagger:model LifecycleStageRequest
//
//nolint:unused // Used by Swagger specification generation.
type swaggerLifecycleStageRequest struct {
	EnvironmentID    *int64 `json:"environment_id"`
	SortOrder        *int64 `json:"sort_order"`
	RequiresApproval *bool  `json:"requires_approval"`
}

// ReorderStagesRequest reorders lifecycle stages.
// swagger:model ReorderStagesRequest
//
//nolint:unused // Used by Swagger specification generation.
type swaggerReorderStagesRequest struct {
	StageIDs []int64 `json:"stage_ids"`
}

// StepRequest is the body for create/update step.
// swagger:model StepRequest
//
//nolint:unused // Used by Swagger specification generation.
type swaggerStepRequest struct {
	Name               string   `json:"name"`
	ScriptBody         string   `json:"script_body"`
	Interpreter        string   `json:"interpreter"`
	SortOrder          int64    `json:"sort_order"`
	TimeoutSeconds     int64    `json:"timeout_seconds"`
	MaxRetries         int64    `json:"max_retries"`
	ExecutionTarget    string   `json:"execution_target"`
	AgentExecutionMode string   `json:"agent_execution_mode"`
	AgentSelectors     []string `json:"agent_selectors"`
	// Required for local steps and agent container mode; rejected for agent host mode.
	ContainerImage       string `json:"container_image"`
	NetworkMode          string `json:"network_mode"`
	ApprovalArtifactPath string `json:"approval_artifact_path"`
	ApprovalReviewPath   string `json:"approval_review_path"`
	ApprovalReviewFormat string `json:"approval_review_format"`
	// Optional variable-name restriction. Empty passes all variables;
	// entries must be identifiers without duplicates.
	VariableNames []string `json:"variable_names"`
}

// ReorderStepsRequest reorders project steps.
// swagger:model ReorderStepsRequest
//
//nolint:unused // Used by Swagger specification generation.
type swaggerReorderStepsRequest struct {
	StepIDs []int64 `json:"step_ids"`
}

// StepTemplateRequest is the body for create/update step template.
// swagger:model StepTemplateRequest
//
//nolint:unused // Used by Swagger specification generation.
type swaggerStepTemplateRequest struct {
	Name               string   `json:"name"`
	ScriptBody         string   `json:"script_body"`
	Interpreter        string   `json:"interpreter"`
	ExecutionTarget    string   `json:"execution_target"`
	AgentExecutionMode string   `json:"agent_execution_mode"`
	AgentSelectors     []string `json:"agent_selectors"`
	// Required for local steps and agent container mode; rejected for agent host mode.
	ContainerImage       string `json:"container_image"`
	NetworkMode          string `json:"network_mode"`
	ApprovalArtifactPath string `json:"approval_artifact_path"`
	ApprovalReviewPath   string `json:"approval_review_path"`
	ApprovalReviewFormat string `json:"approval_review_format"`
	// Optional variable-name restriction. Empty passes all variables;
	// entries must be identifiers without duplicates.
	VariableNames []string `json:"variable_names"`
}

// VariableRequest is the body for create/update variable.
//
// On update, an empty value with secret=true keeps the stored secret
// (the API never returns secret plaintext, so clients send the masked
// read-back unchanged); set secret=false to clear it.
//
// swagger:model VariableRequest
//
//nolint:unused // Used by Swagger specification generation.
type swaggerVariableRequest struct {
	Name          string `json:"name"`
	Value         string `json:"value"`
	EnvironmentID *int64 `json:"environment_id"`
	Secret        bool   `json:"secret"`
}

// VariableResponse is the JSON shape for a variable.
//
// Value is always an empty string when Secret is 1: ordinary reads
// never return secret plaintext (issue #29).
//
// swagger:model VariableResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerVariableResponse struct {
	ID            int64  `json:"id"`
	ProjectID     int64  `json:"project_id"`
	Name          string `json:"name"`
	Value         string `json:"value"`
	EnvironmentID *int64 `json:"environment_id"`
	CreatedAt     int64  `json:"created_at"`
	Secret        int64  `json:"secret"`
}

// ReleaseRequest is the body for create release.
// swagger:model ReleaseRequest
//
//nolint:unused // Used by Swagger specification generation.
type swaggerReleaseRequest struct {
	// Attached packages are downloaded and pinned on the first deployment pull.
	Version string `json:"version"`
}

// ReleaseWithVariablesResponse is the detailed release payload.
// swagger:model ReleaseWithVariablesResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerReleaseWithVariablesResponse struct {
	ID        int64                            `json:"id"`
	ProjectID int64                            `json:"project_id"`
	Version   string                           `json:"version"`
	StepsJSON string                           `json:"steps_json"`
	CreatedAt int64                            `json:"created_at"`
	Variables []swaggerReleaseVariableResponse `json:"variables"`
}

// ReleaseVariableResponse is a variable snapshot in a release.
//
// Value is always a string. It is an empty string whenever Secret
// is 1 or the variable has no stored value; ordinary reads never
// return secret plaintext (issue #29).
//
// swagger:model ReleaseVariableResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerReleaseVariableResponse struct {
	ID            int64                 `json:"id"`
	ReleaseID     int64                 `json:"release_id"`
	Name          string                `json:"name"`
	Value         string                `json:"value"`
	EnvironmentID swaggerSQLNullInteger `json:"environment_id"`
	Secret        int64                 `json:"secret"`
}

// ScheduleRequest is the body for create/update scheduled deployment.
// swagger:model ScheduleRequest
//
//nolint:unused // Used by Swagger specification generation.
type swaggerScheduleRequest struct {
	ReleaseID     int64  `json:"release_id"`
	EnvironmentID int64  `json:"environment_id"`
	Cron          string `json:"cron"`
	// Deprecated: alias for cron. Prefer cron in new callers.
	CronExpr string `json:"cron_expr"`
	Enabled  bool   `json:"enabled"`
	// Active is an alias for enabled; either is accepted.
	Active bool   `json:"active"`
	Note   string `json:"note"`
}

// ProjectMemberRequest is the body for adding a project member.
// swagger:model ProjectMemberRequest
//
//nolint:unused // Used by Swagger specification generation.
type swaggerProjectMemberRequest struct {
	UserID int64  `json:"user_id"`
	Role   string `json:"role"`
}

// ProjectMemberResponse is the JSON shape for project membership.
// swagger:model ProjectMemberResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerProjectMemberResponse struct {
	ProjectID int64  `json:"project_id"`
	UserID    int64  `json:"user_id"`
	Role      string `json:"role"`
	CreatedAt int64  `json:"created_at"`
	Email     string `json:"email"`
	Name      string `json:"name"`
}

// UserRequest is the body for create/update user.
// swagger:model UserRequest
//
//nolint:unused // Used by Swagger specification generation.
type swaggerUserRequest struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

// UserResponse is the JSON shape for a user.
// swagger:model UserResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerUserResponse struct {
	ID              int64  `json:"id"`
	Email           string `json:"email"`
	Name            string `json:"name"`
	Role            string `json:"role"`
	CreatedAt       int64  `json:"created_at"`
	UpdatedAt       int64  `json:"updated_at"`
	LastLoginAt     *int64 `json:"last_login_at"`
	OneTimePassword string `json:"one_time_password,omitempty"`
}

// UserListResponse is a list of users.
// swagger:model UserListResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerUserListResponse []swaggerUserResponse

// GlobalNotificationRequest is the body for global notification settings.
// swagger:model GlobalNotificationRequest
//
//nolint:unused // Used by Swagger specification generation.
type swaggerGlobalNotificationRequest struct {
	ClearSlackWebhookURL   bool   `json:"clear_slack_webhook_url"`
	ClearGotifyToken       bool   `json:"clear_gotify_token"`
	ClearDiscordWebhookURL bool   `json:"clear_discord_webhook_url"`
	SlackWebhookURL        string `json:"slack_webhook_url"`
	NotifyEmails           string `json:"notify_emails"`
	GotifyURL              string `json:"gotify_url"`
	GotifyToken            string `json:"gotify_token"`
	DiscordWebhookURL      string `json:"discord_webhook_url"`
}

// EmptyResponse is returned for no-content endpoints.
// swagger:model EmptyResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerEmptyResponse struct{}

// DeploymentScheduleRequest is the body for scheduling a deployment.
// swagger:model DeploymentScheduleRequest
//
//nolint:unused // Used by Swagger specification generation.
type swaggerDeploymentScheduleRequest struct {
	ReleaseID     int64  `json:"release_id"`
	EnvironmentID int64  `json:"environment_id"`
	Force         bool   `json:"force"`
	Note          string `json:"note"`
}

// Array response models.

// TokenListResponse is a list of API tokens.
// swagger:model TokenListResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerTokenListResponse []swaggerTokenResponse

// EnvironmentListResponse is a list of environments.
// swagger:model EnvironmentListResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerEnvironmentListResponse []swaggerEnvironment

// LifecycleListResponse is a list of lifecycles.
// swagger:model LifecycleListResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerLifecycleListResponse []swaggerLifecycle

// LifecycleStageListResponse is a list of lifecycle stages.
// swagger:model LifecycleStageListResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerLifecycleStageListResponse []swaggerLifecycleStage

// StepListResponse is a list of project steps.
// swagger:model StepListResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerStepListResponse []swaggerStep

// StepTemplateListResponse is a list of step templates.
// swagger:model StepTemplateListResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerStepTemplateListResponse []swaggerStepTemplate

// StepTemplateVersionListResponse is a list of step template versions.
// swagger:model StepTemplateVersionListResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerStepTemplateVersionListResponse []swaggerStepTemplateVersion

// ReleaseListResponse is a list of releases.
// swagger:model ReleaseListResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerReleaseListResponse []swaggerRelease

// VariableListResponse is a list of variables.
// swagger:model VariableListResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerVariableListResponse []swaggerVariableResponse

// ScheduledDeploymentListResponse is a list of scheduled deployments.
// swagger:model ScheduledDeploymentListResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerScheduledDeploymentListResponse []swaggerScheduledDeployment

// AuditLogListResponse is a list of audit log entries.
// swagger:model AuditLogListResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerAuditLogListResponse []swaggerAuditLog

// NotificationEventListResponse is a list of notification events.
// swagger:model NotificationEventListResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerNotificationEventListResponse []swaggerNotificationEvent

// ProjectMemberListResponse is a list of project members.
// swagger:model ProjectMemberListResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerProjectMemberListResponse []swaggerProjectMemberResponse

// LogLineListResponse is a list of streamed log lines.
// swagger:model LogLineListResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerLogLineListResponse []swaggerLogEntry

// DeploymentLog is a single step log entry.
// swagger:model DeploymentLog
//
//nolint:unused // Used by Swagger specification generation.
type swaggerDeploymentLog struct {
	ID           int64  `json:"id"`
	DeploymentID int64  `json:"deployment_id"`
	StepName     string `json:"step_name"`
	Line         string `json:"line"`
	CreatedAt    int64  `json:"created_at"`
}

// DeploymentLogListResponse is a list of deployment log entries.
// swagger:model DeploymentLogListResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerDeploymentLogListResponse []swaggerDeploymentLog

// ProjectMemberRoleRequest updates a member's role.
// swagger:model ProjectMemberRoleRequest
//
//nolint:unused // Used by Swagger specification generation.
type swaggerProjectMemberRoleRequest struct {
	Role string `json:"role"`
}

// ServerStatsResponse contains aggregate counts.
// swagger:model ServerStatsResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerServerStatsResponse struct {
	Users       int64 `json:"users"`
	Projects    int64 `json:"projects"`
	Deployments int64 `json:"deployments"`
}

// DbTableListResponse is a list of SQLite table names.
// swagger:model DbTableListResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerDbTableListResponse []string

// StreamResponse is a server-sent event stream.
// swagger:model StreamResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerStreamResponse struct {
	Data string `json:"data"`
}

// swagger:model Agent
//
//nolint:unused // Used by Swagger specification generation.
type swaggerAgent struct {
	AdministrativeStatus     string                `json:"administrative_status"`
	Draining                 bool                  `json:"draining"`
	AgentProtocol            swaggerSQLNullString  `json:"agent_protocol"`
	Health                   string                `json:"health"`
	CurrentWork              []swaggerAgentWork    `json:"current_work"`
	QueuedWork               int64                 `json:"queued_work"`
	LastSuccessfulDeployment *swaggerAgentSuccess  `json:"last_successful_deployment"`
	LastError                *swaggerAgentError    `json:"last_error"`
	ServerVersion            string                `json:"server_version"`
	RecommendedAgentVersion  string                `json:"recommended_agent_version"`
	Compatibility            string                `json:"compatibility"`
	ID                       string                `json:"id"`
	Name                     string                `json:"name"`
	Endpoint                 string                `json:"endpoint"`
	Status                   string                `json:"status"`
	AgentVersion             swaggerSQLNullString  `json:"agent_version"`
	CertificateFingerprint   swaggerSQLNullString  `json:"certificate_fingerprint"`
	LastHeartbeatAt          swaggerSQLNullInteger `json:"last_heartbeat_at"`
	RevokedAt                swaggerSQLNullInteger `json:"revoked_at"`
	CreatedAt                int64                 `json:"created_at"`
	UpdatedAt                int64                 `json:"updated_at"`
	Interpreters             []string              `json:"interpreters"`
	ExecutionModes           []string              `json:"execution_modes"`
	ContainerRuntimes        []string              `json:"container_runtimes"`
	ContainerInterpreters    []string              `json:"container_interpreters"`
}

//nolint:unused // Used by Swagger schema declarations.
type swaggerSQLNullString struct {
	String string `json:"String"`
	Valid  bool   `json:"Valid"`
}

//nolint:unused // Used by Swagger schema declarations.
type swaggerSQLNullInteger struct {
	Int64 int64 `json:"Int64"`
	Valid bool  `json:"Valid"`
}

// swagger:model AgentListResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerAgentListResponse []swaggerAgent

// swagger:model AgentDetailResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerAgentDetailResponse struct {
	Agent             swaggerAgent         `json:"agent"`
	Labels            []string             `json:"labels"`
	EnvironmentLabels []swaggerEnvironment `json:"environment_labels"`
}

//nolint:unused // Used by Swagger schema declarations.
type swaggerAgentWork struct {
	DeploymentID int64  `json:"deployment_id"`
	StepIndex    int64  `json:"step_index"`
	State        string `json:"state"`
}

//nolint:unused // Used by Swagger schema declarations.
type swaggerAgentSuccess struct {
	DeploymentID int64                 `json:"deployment_id"`
	FinishedAt   swaggerSQLNullInteger `json:"finished_at"`
}

//nolint:unused // Used by Swagger schema declarations.
type swaggerAgentError struct {
	DeploymentID int64                 `json:"deployment_id"`
	StepIndex    int64                 `json:"step_index"`
	State        string                `json:"state"`
	Reason       string                `json:"reason"`
	FinishedAt   swaggerSQLNullInteger `json:"finished_at"`
}

// swagger:model AgentEnvironmentRequest
//
//nolint:unused // Used by Swagger specification generation.
type swaggerAgentEnvironmentRequest struct {
	EnvironmentID int64 `json:"environment_id"`
}

// swagger:model PairAgentRequest
//
//nolint:unused // Used by Swagger specification generation.
type swaggerPairAgentRequest struct {
	Address     string `json:"address"`
	Code        string `json:"code"`
	Fingerprint string `json:"fingerprint"`
}

// swagger:model PairAgentResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerPairAgentResponse struct {
	AgentID string `json:"agent_id"`
	State   string `json:"state"`
}

// swagger:model AgentLabelRequest
//
//nolint:unused // Used by Swagger specification generation.
type swaggerAgentLabelRequest struct {
	Label string `json:"label"`
}

// swagger:model RunbookStep
// RunbookStep is one script in an immutable runbook version.
//
//nolint:unused // Used by Swagger specification generation.
type swaggerRunbookStep struct {
	Name                 string   `json:"name"`
	ScriptBody           string   `json:"script_body"`
	Interpreter          string   `json:"interpreter"`
	SortOrder            int64    `json:"sort_order"`
	TimeoutSeconds       int64    `json:"timeout_seconds"`
	MaxRetries           int64    `json:"max_retries"`
	ExecutionTarget      string   `json:"execution_target"`
	AgentExecutionMode   string   `json:"agent_execution_mode"`
	AgentSelectors       []string `json:"agent_selectors"`
	ContainerImage       string   `json:"container_image"`
	NetworkMode          string   `json:"network_mode"`
	ApprovalArtifactPath string   `json:"approval_artifact_path"`
	ApprovalReviewPath   string   `json:"approval_review_path"`
	ApprovalReviewFormat string   `json:"approval_review_format"`
	VariableNames        []string `json:"variable_names"`
}

// swagger:model RunbookSaveRequest
// RunbookSaveRequest creates a runbook or saves its next version.
//
//nolint:unused // Used by Swagger specification generation.
type swaggerRunbookSaveRequest struct {
	KeepArtifactPin   bool                 `json:"keep_artifact_pin"`
	ArtifactReleaseID int64                `json:"artifact_release_id"`
	Name              string               `json:"name"`
	Description       string               `json:"description"`
	Steps             []swaggerRunbookStep `json:"steps"`
}

// swagger:model Runbook
// Runbook is a named collection of immutable versions.
//
//nolint:unused // Used by Swagger specification generation.
type swaggerRunbook struct {
	DeletedAt   swaggerSQLNullInteger `json:"deleted_at"`
	ID          int64                 `json:"id"`
	ProjectID   int64                 `json:"project_id"`
	Name        string                `json:"name"`
	Description string                `json:"description"`
	CreatedAt   int64                 `json:"created_at"`
}

// swagger:model RunbookVersion
// RunbookVersion identifies a release-backed snapshot.
//
//nolint:unused // Used by Swagger specification generation.
type swaggerRunbookVersion struct {
	ID        int64 `json:"id"`
	RunbookID int64 `json:"runbook_id"`
	Version   int64 `json:"version"`
	ReleaseID int64 `json:"release_id"`
	CreatedAt int64 `json:"created_at"`
}

// swagger:model RunbookSaveResponse
// RunbookSaveResponse contains the named runbook and saved version.
//
//nolint:unused // Used by Swagger specification generation.
type swaggerRunbookSaveResponse struct {
	Runbook swaggerRunbook        `json:"runbook"`
	Version swaggerRunbookVersion `json:"version"`
}

// swagger:model RunbookExecuteRequest
// RunbookExecuteRequest chooses an environment and optional pinned version.
//
//nolint:unused // Used by Swagger specification generation.
type swaggerRunbookExecuteRequest struct {
	EnvironmentID int64 `json:"environment_id"`
	VersionID     int64 `json:"version_id"`
}

// swagger:model RunbookExecution
// RunbookExecution records the concrete version behind a deployment.
//
//nolint:unused // Used by Swagger specification generation.
type swaggerRunbookExecution struct {
	ID               int64                 `json:"id"`
	RunbookVersionID int64                 `json:"runbook_version_id"`
	DeploymentID     int64                 `json:"deployment_id"`
	ActorUserID      swaggerSQLNullInteger `json:"actor_user_id"`
	ScheduleID       swaggerSQLNullInteger `json:"schedule_id"`
	CreatedAt        int64                 `json:"created_at"`
}

// swagger:model RunbookScheduleRequest
// RunbookScheduleRequest pins a version or follows the latest version.
//
//nolint:unused // Used by Swagger specification generation.
type swaggerRunbookScheduleRequest struct {
	EnvironmentID int64  `json:"environment_id"`
	VersionID     int64  `json:"version_id"`
	Cron          string `json:"cron"`
}

// swagger:model RunbookSchedule
// RunbookSchedule is a saved cron trigger for a runbook.
//
//nolint:unused // Used by Swagger specification generation.
type swaggerRunbookSchedule struct {
	ID            int64                 `json:"id"`
	RunbookID     int64                 `json:"runbook_id"`
	VersionID     swaggerSQLNullInteger `json:"version_id"`
	EnvironmentID int64                 `json:"environment_id"`
	Cron          string                `json:"cron"`
	NextRunAt     int64                 `json:"next_run_at"`
	Enabled       int64                 `json:"enabled"`
	LastFiredAt   swaggerSQLNullInteger `json:"last_fired_at"`
	LastError     string                `json:"last_error"`
	CreatedAt     int64                 `json:"created_at"`
}

// swagger:model RunbookListResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerRunbookListResponse []swaggerRunbook

// swagger:model RunbookDetailResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerRunbookDetailResponse struct {
	Runbook  swaggerRunbook          `json:"runbook"`
	Versions []swaggerRunbookVersion `json:"versions"`
}

// swagger:model RunbookVersionResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerRunbookVersionResponse struct {
	Artifact *releaseArtifactResponse `json:"artifact"`
	Version  swaggerRunbookVersion    `json:"version"`
	Steps    []swaggerRunbookStep     `json:"steps"`
}

// swagger:model RunbookExecutionDetail
//
//nolint:unused // Used by Swagger specification generation.
type swaggerRunbookExecutionDetail struct {
	RunbookDeletedAt   swaggerSQLNullInteger `json:"runbook_deleted_at"`
	QueuePosition      int64                 `json:"queue_position"`
	ActiveDeploymentID int64                 `json:"active_deployment_id,omitempty"`
	ActiveWorkURL      string                `json:"active_work_url,omitempty"`
	ID                 int64                 `json:"id"`
	RunbookVersionID   int64                 `json:"runbook_version_id"`
	DeploymentID       int64                 `json:"deployment_id"`
	ActorUserID        swaggerSQLNullInteger `json:"actor_user_id"`
	ScheduleID         swaggerSQLNullInteger `json:"schedule_id"`
	CreatedAt          int64                 `json:"created_at"`
	EnvironmentID      int64                 `json:"environment_id"`
	Status             string                `json:"status"`
	StartedAt          swaggerSQLNullInteger `json:"started_at"`
	FinishedAt         swaggerSQLNullInteger `json:"finished_at"`
	RunbookID          int64                 `json:"runbook_id"`
	Version            int64                 `json:"version"`
	ProjectID          int64                 `json:"project_id"`
	RunbookName        string                `json:"runbook_name"`
	EnvironmentName    string                `json:"environment_name"`
}

// swagger:model RunbookExecutionListResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerRunbookExecutionListResponse struct {
	Items  []swaggerRunbookExecutionDetail `json:"items"`
	Total  int64                           `json:"total"`
	Limit  int64                           `json:"limit"`
	Offset int64                           `json:"offset"`
}

// swagger:model RunbookScheduleListResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerRunbookScheduleListResponse []swaggerRunbookSchedule

// swagger:model RunbookStateResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerRunbookStateResponse struct {
	Status string `json:"status"`
}

// swagger:model RunbookScheduleStateResponse
//
//nolint:unused // Used by Swagger specification generation.
type swaggerRunbookScheduleStateResponse struct {
	Enabled bool `json:"enabled"`
}
