// allow: SIZE_OK — swagger model definitions for the generated OpenAPI spec.
package api

// --- Database-mirror models returned directly by API handlers ---

// Environment represents a deployment target.
// swagger:model Environment
type swaggerEnvironment struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Tags        *string `json:"tags"`
	CreatedAt   int64   `json:"created_at"`
}

// Lifecycle represents a deployment promotion pipeline.
// swagger:model Lifecycle
type swaggerLifecycle struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	CreatedAt   int64   `json:"created_at"`
}

// LifecycleStage is a single stage in a lifecycle.
// swagger:model LifecycleStage
type swaggerLifecycleStage struct {
	ID               int64 `json:"id"`
	LifecycleID      int64 `json:"lifecycle_id"`
	EnvironmentID    int64 `json:"environment_id"`
	SortOrder        int64 `json:"sort_order"`
	RequiresApproval int64 `json:"requires_approval"`
}

// Step is a bash script step within a project.
// swagger:model Step
type swaggerStep struct {
	ID             int64  `json:"id"`
	ProjectID      int64  `json:"project_id"`
	Name           string `json:"name"`
	ScriptBody     string `json:"script_body"`
	SortOrder      int64  `json:"sort_order"`
	CreatedAt      int64  `json:"created_at"`
	TimeoutSeconds int64  `json:"timeout_seconds"`
	MaxRetries     int64  `json:"max_retries"`
}

// StepTemplate is a reusable step template.
// swagger:model StepTemplate
type swaggerStepTemplate struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	ScriptBody string `json:"script_body"`
	CreatedAt  int64  `json:"created_at"`
}

// StepTemplateVersion is a historical version of a step template.
// swagger:model StepTemplateVersion
type swaggerStepTemplateVersion struct {
	ID            int64  `json:"id"`
	TemplateID    int64  `json:"template_id"`
	VersionNumber int64  `json:"version_number"`
	Name          string `json:"name"`
	ScriptBody    string `json:"script_body"`
	CreatedAt     int64  `json:"created_at"`
}

// Release is an immutable snapshot of project steps and variables.
// swagger:model Release
type swaggerRelease struct {
	ID        int64  `json:"id"`
	ProjectID int64  `json:"project_id"`
	Version   string `json:"version"`
	StepsJSON string `json:"steps_json"`
	CreatedAt int64  `json:"created_at"`
}

// Deployment represents a release executing against an environment.
// swagger:model Deployment
type swaggerDeployment struct {
	ID            int64                     `json:"id"`
	ReleaseID     int64                     `json:"release_id"`
	EnvironmentID int64                     `json:"environment_id"`
	Status        string                    `json:"status"`
	StartedAt     *int64                    `json:"started_at"`
	FinishedAt    *int64                    `json:"finished_at"`
	CreatedAt     int64                     `json:"created_at"`
	Forced        int64                     `json:"forced"`
	Note          *string                   `json:"note"`
	Dispatch      swaggerDeploymentDispatch `json:"dispatch"`
}

// DeploymentDispatch is the safe operator-facing routing state.
// swagger:model DeploymentDispatch
type swaggerDeploymentDispatch struct {
	Mode   string                  `json:"mode"`
	State  string                  `json:"state,omitempty"`
	Reason string                  `json:"reason,omitempty"`
	Agent  *swaggerDeploymentAgent `json:"agent,omitempty"`
}

// DeploymentAgent is the assigned agent's safe health metadata.
// swagger:model DeploymentAgent
type swaggerDeploymentAgent struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Status          string `json:"status"`
	LastHeartbeatAt *int64 `json:"last_heartbeat_at,omitempty"`
}

// DeploymentListItem is the enriched row returned by ListDeployments.
// swagger:model DeploymentListItem
type swaggerDeploymentListItem struct {
	ID              int64                     `json:"id"`
	ReleaseID       int64                     `json:"release_id"`
	EnvironmentID   int64                     `json:"environment_id"`
	Status          string                    `json:"status"`
	StartedAt       *int64                    `json:"started_at"`
	FinishedAt      *int64                    `json:"finished_at"`
	CreatedAt       int64                     `json:"created_at"`
	Forced          int64                     `json:"forced"`
	Note            *string                   `json:"note"`
	ProjectName     string                    `json:"project_name"`
	ReleaseVersion  string                    `json:"release_version"`
	EnvironmentName string                    `json:"environment_name"`
	Dispatch        swaggerDeploymentDispatch `json:"dispatch"`
}

// DeploymentListResponse is the paginated envelope for ListDeployments.
// swagger:model DeploymentListResponse
type swaggerDeploymentListResponse struct {
	Items  []swaggerDeploymentListItem `json:"items"`
	Total  int64                       `json:"total"`
	Limit  int64                       `json:"limit"`
	Offset int64                       `json:"offset"`
}

// DeploymentStatusResponse is the status payload for GetDeploymentStatus.
// swagger:model DeploymentStatusResponse
type swaggerDeploymentStatusResponse struct {
	Status   string                    `json:"status"`
	Dispatch swaggerDeploymentDispatch `json:"dispatch"`
}

// ScheduledDeployment is a cron-driven deployment configuration.
// swagger:model ScheduledDeployment
type swaggerScheduledDeployment struct {
	ID            int64   `json:"id"`
	ProjectID     int64   `json:"project_id"`
	ReleaseID     int64   `json:"release_id"`
	EnvironmentID int64   `json:"environment_id"`
	Cron          string  `json:"cron"`
	NextRunAt     int64   `json:"next_run_at"`
	Enabled       int64   `json:"enabled"`
	LastFiredAt   *int64  `json:"last_fired_at"`
	Note          *string `json:"note"`
	CreatedAt     int64   `json:"created_at"`
	UpdatedAt     int64   `json:"updated_at"`
}

// AuditLog is a recorded state-changing action.
// swagger:model AuditLog
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
type swaggerProjectMember struct {
	ProjectID int64  `json:"project_id"`
	UserID    int64  `json:"user_id"`
	Role      string `json:"role"`
	CreatedAt int64  `json:"created_at"`
}

// User is a user account.
// swagger:model User
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
type swaggerGlobalNotification struct {
	ID                int64   `json:"id"`
	SlackWebhookURL   *string `json:"slack_webhook_url"`
	NotifyEmails      *string `json:"notify_emails"`
	GotifyURL         *string `json:"gotify_url"`
	GotifyToken       *string `json:"gotify_token"`
	DiscordWebhookURL *string `json:"discord_webhook_url"`
	UpdatedAt         int64   `json:"updated_at"`
}

// Variable is a project or environment-scoped variable.
// swagger:model Variable
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
type swaggerLogEntry struct {
	TS   int64  `json:"ts"`
	Step string `json:"step"`
	Line string `json:"line"`
}

// LogStreamParams controls the format of StreamLogs.
// swagger:model LogStreamParams
type swaggerLogStreamParams struct {
	// Format is either sse or ndjson.
	//
	// in: query
	Format string `json:"format"`
}

// AuditLogParams filters the audit log list.
// swagger:model AuditLogParams
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
type swaggerTokenResponse struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Prefix     string `json:"prefix"`
	Scope      string `json:"scope"`
	UserID     int64  `json:"user_id,omitempty"`
	Email      string `json:"email,omitempty"`
	UserName   string `json:"user_name,omitempty"`
	LastUsedAt *int64 `json:"last_used_at,omitempty"`
	ExpiresAt  *int64 `json:"expires_at,omitempty"`
	CreatedAt  int64  `json:"created_at"`
	RevokedAt  *int64 `json:"revoked_at,omitempty"`
}

// CreateTokenResponse returns the plaintext token once.
// swagger:model CreateTokenResponse
type swaggerCreateTokenResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Prefix    string `json:"prefix"`
	CreatedAt int64  `json:"created_at"`
}

// CreateTokenRequest is the body for CreateToken.
// swagger:model CreateTokenRequest
type swaggerCreateTokenRequest struct {
	Name string `json:"name"`
}

// ProjectListResponse is a list of projects.
// swagger:model ProjectListResponse
type swaggerProjectListResponse []swaggerProjectResponse

// ProjectResponse is the JSON shape for a project.
// swagger:model ProjectResponse
type swaggerProjectResponse struct {
	ID                int64  `json:"id"`
	Name              string `json:"name"`
	Description       string `json:"description"`
	CreatedAt         int64  `json:"created_at"`
	LifecycleID       *int64 `json:"lifecycle_id"`
	SlackWebhookURL   string `json:"slack_webhook_url"`
	NotifyEmails      string `json:"notify_emails"`
	GotifyURL         string `json:"gotify_url"`
	GotifyToken       string `json:"gotify_token"`
	DiscordWebhookURL string `json:"discord_webhook_url"`
}

// ProjectNotificationResponse is the notification settings for a project.
// swagger:model ProjectNotificationResponse
type swaggerProjectNotificationResponse struct {
	ID                int64  `json:"id"`
	Name              string `json:"name"`
	Description       string `json:"description"`
	SlackWebhookURL   string `json:"slack_webhook_url"`
	NotifyEmails      string `json:"notify_emails"`
	GotifyURL         string `json:"gotify_url"`
	GotifyToken       string `json:"gotify_token"`
	DiscordWebhookURL string `json:"discord_webhook_url"`
	CreatedAt         int64  `json:"created_at"`
}

// ProjectNotificationRequest updates a project's notification settings.
// swagger:model ProjectNotificationRequest
type swaggerProjectNotificationRequest struct {
	SlackWebhookURL   string `json:"slack_webhook_url"`
	NotifyEmails      string `json:"notify_emails"`
	GotifyURL         string `json:"gotify_url"`
	GotifyToken       string `json:"gotify_token"`
	DiscordWebhookURL string `json:"discord_webhook_url"`
}

// ProjectRequest is the body for create/update project.
// swagger:model ProjectRequest
type swaggerProjectRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	LifecycleID int64  `json:"lifecycle_id"`
}

// EnvironmentRequest is the body for create/update environment.
// swagger:model EnvironmentRequest
type swaggerEnvironmentRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Tags        string `json:"tags"`
}

// LifecycleRequest is the body for create/update lifecycle.
// swagger:model LifecycleRequest
type swaggerLifecycleRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// LifecycleResponse is the detailed lifecycle payload.
// swagger:model LifecycleResponse
type swaggerLifecycleResponse struct {
	Lifecycle swaggerLifecycle        `json:"lifecycle"`
	Stages    []swaggerLifecycleStage `json:"stages"`
}

// LifecycleStageRequest is the body for add/update lifecycle stage.
// swagger:model LifecycleStageRequest
type swaggerLifecycleStageRequest struct {
	EnvironmentID    *int64 `json:"environment_id"`
	SortOrder        *int64 `json:"sort_order"`
	RequiresApproval *bool  `json:"requires_approval"`
}

// ReorderStagesRequest reorders lifecycle stages.
// swagger:model ReorderStagesRequest
type swaggerReorderStagesRequest struct {
	StageIDs []int64 `json:"stage_ids"`
}

// StepRequest is the body for create/update step.
// swagger:model StepRequest
type swaggerStepRequest struct {
	Name           string `json:"name"`
	ScriptBody     string `json:"script_body"`
	SortOrder      int64  `json:"sort_order"`
	TimeoutSeconds int64  `json:"timeout_seconds"`
	MaxRetries     int64  `json:"max_retries"`
}

// ReorderStepsRequest reorders project steps.
// swagger:model ReorderStepsRequest
type swaggerReorderStepsRequest struct {
	StepIDs []int64 `json:"step_ids"`
}

// StepTemplateRequest is the body for create/update step template.
// swagger:model StepTemplateRequest
type swaggerStepTemplateRequest struct {
	Name       string `json:"name"`
	ScriptBody string `json:"script_body"`
}

// VariableRequest is the body for create/update variable.
// swagger:model VariableRequest
type swaggerVariableRequest struct {
	Name          string `json:"name"`
	Value         string `json:"value"`
	EnvironmentID *int64 `json:"environment_id"`
	Secret        bool   `json:"secret"`
}

// VariableResponse is the JSON shape for a variable.
// swagger:model VariableResponse
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
type swaggerReleaseRequest struct {
	Version string `json:"version"`
}

// ReleaseWithVariablesResponse is the detailed release payload.
// swagger:model ReleaseWithVariablesResponse
type swaggerReleaseWithVariablesResponse struct {
	ID        int64                            `json:"id"`
	ProjectID int64                            `json:"project_id"`
	Version   string                           `json:"version"`
	StepsJSON string                           `json:"steps_json"`
	CreatedAt int64                            `json:"created_at"`
	Variables []swaggerReleaseVariableResponse `json:"variables"`
}

// ReleaseVariableResponse is a variable snapshot in a release.
// swagger:model ReleaseVariableResponse
type swaggerReleaseVariableResponse struct {
	ID            int64   `json:"id"`
	ReleaseID     int64   `json:"release_id"`
	Name          string  `json:"name"`
	Value         *string `json:"value"`
	EnvironmentID *int64  `json:"environment_id"`
	Secret        int64   `json:"secret"`
}

// ScheduleRequest is the body for create/update scheduled deployment.
// swagger:model ScheduleRequest
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
type swaggerProjectMemberRequest struct {
	UserID int64  `json:"user_id"`
	Role   string `json:"role"`
}

// ProjectMemberResponse is the JSON shape for project membership.
// swagger:model ProjectMemberResponse
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
type swaggerUserRequest struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

// UserResponse is the JSON shape for a user.
// swagger:model UserResponse
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
type swaggerUserListResponse []swaggerUserResponse

// GlobalNotificationRequest is the body for global notification settings.
// swagger:model GlobalNotificationRequest
type swaggerGlobalNotificationRequest struct {
	SlackWebhookURL   string `json:"slack_webhook_url"`
	NotifyEmails      string `json:"notify_emails"`
	GotifyURL         string `json:"gotify_url"`
	GotifyToken       string `json:"gotify_token"`
	DiscordWebhookURL string `json:"discord_webhook_url"`
}

// EmptyResponse is returned for no-content endpoints.
// swagger:model EmptyResponse
type swaggerEmptyResponse struct{}

// DeploymentScheduleRequest is the body for scheduling a deployment.
// swagger:model DeploymentScheduleRequest
type swaggerDeploymentScheduleRequest struct {
	ReleaseID     int64  `json:"release_id"`
	EnvironmentID int64  `json:"environment_id"`
	Force         bool   `json:"force"`
	Note          string `json:"note"`
}

// Array response models.

// TokenListResponse is a list of API tokens.
// swagger:model TokenListResponse
type swaggerTokenListResponse []swaggerTokenResponse

// EnvironmentListResponse is a list of environments.
// swagger:model EnvironmentListResponse
type swaggerEnvironmentListResponse []swaggerEnvironment

// LifecycleListResponse is a list of lifecycles.
// swagger:model LifecycleListResponse
type swaggerLifecycleListResponse []swaggerLifecycle

// LifecycleStageListResponse is a list of lifecycle stages.
// swagger:model LifecycleStageListResponse
type swaggerLifecycleStageListResponse []swaggerLifecycleStage

// StepListResponse is a list of project steps.
// swagger:model StepListResponse
type swaggerStepListResponse []swaggerStep

// StepTemplateListResponse is a list of step templates.
// swagger:model StepTemplateListResponse
type swaggerStepTemplateListResponse []swaggerStepTemplate

// StepTemplateVersionListResponse is a list of step template versions.
// swagger:model StepTemplateVersionListResponse
type swaggerStepTemplateVersionListResponse []swaggerStepTemplateVersion

// ReleaseListResponse is a list of releases.
// swagger:model ReleaseListResponse
type swaggerReleaseListResponse []swaggerRelease

// VariableListResponse is a list of variables.
// swagger:model VariableListResponse
type swaggerVariableListResponse []swaggerVariableResponse

// ScheduledDeploymentListResponse is a list of scheduled deployments.
// swagger:model ScheduledDeploymentListResponse
type swaggerScheduledDeploymentListResponse []swaggerScheduledDeployment

// AuditLogListResponse is a list of audit log entries.
// swagger:model AuditLogListResponse
type swaggerAuditLogListResponse []swaggerAuditLog

// NotificationEventListResponse is a list of notification events.
// swagger:model NotificationEventListResponse
type swaggerNotificationEventListResponse []swaggerNotificationEvent

// ProjectMemberListResponse is a list of project members.
// swagger:model ProjectMemberListResponse
type swaggerProjectMemberListResponse []swaggerProjectMemberResponse

// LogLineListResponse is a list of streamed log lines.
// swagger:model LogLineListResponse
type swaggerLogLineListResponse []swaggerLogEntry

// DeploymentLog is a single step log entry.
// swagger:model DeploymentLog
type swaggerDeploymentLog struct {
	ID           int64  `json:"id"`
	DeploymentID int64  `json:"deployment_id"`
	StepName     string `json:"step_name"`
	Line         string `json:"line"`
	CreatedAt    int64  `json:"created_at"`
}

// DeploymentLogListResponse is a list of deployment log entries.
// swagger:model DeploymentLogListResponse
type swaggerDeploymentLogListResponse []swaggerDeploymentLog

// ProjectMemberRoleRequest updates a member's role.
// swagger:model ProjectMemberRoleRequest
type swaggerProjectMemberRoleRequest struct {
	Role string `json:"role"`
}

// ServerStatsResponse contains aggregate counts.
// swagger:model ServerStatsResponse
type swaggerServerStatsResponse struct {
	Users       int64 `json:"users"`
	Projects    int64 `json:"projects"`
	Deployments int64 `json:"deployments"`
}

// DbTableListResponse is a list of SQLite table names.
// swagger:model DbTableListResponse
type swaggerDbTableListResponse []string

// StreamResponse is a server-sent event stream.
// swagger:model StreamResponse
type swaggerStreamResponse struct {
	Data string `json:"data"`
}
