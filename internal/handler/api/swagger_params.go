// allow: SIZE_OK — swagger parameter structs for the generated OpenAPI spec.
package api

// ID path parameter. Linked to every operation whose path contains an {id} segment.
// swagger:parameters addLifecycleStage addMember adminDeleteUser adminGetUser adminUpdateUser approveDeployment cancelDeployment createDeployment createRelease createSchedule createStep createVariable deleteEnvironment deleteLifecycleStage deleteProject deleteSchedule deleteStep deleteTemplate deleteVariable deploymentEvents exportLogs getDeployment getDeploymentStatus getEnvironment getLifecycle getLog getProject getProjectNotifications getRelease getSchedule getStep getTemplate getTemplateHistory getVariable listDeploymentLogs listDeployments listMembers listReleases listSchedules listSteps listVariables redeployDeployment refreshRelease removeMember reorderLifecycleStages reorderSteps retryDeployment revokeAnyToken revokeToken saveLifecycle streamLogs templatesPicker toggleSchedule updateEnvironment updateLifecycleStage updateMemberRole updateProject updateProjectNotifications updateSchedule updateStep updateTemplate updateVariable
type idPathParam struct {
	// The resource ID.
	//
	// in: path
	// required: true
	ID int64 `json:"id"`
}

// stepId path parameter.
// swagger:parameters getStep updateStep deleteStep
type stepIDPathParam struct {
	// The step ID.
	//
	// in: path
	// required: true
	StepID int64 `json:"stepId"`
}

// relId path parameter.
// swagger:parameters getRelease refreshRelease
type relIDPathParam struct {
	// The release ID.
	//
	// in: path
	// required: true
	RelID int64 `json:"relId"`
}

// schedId path parameter.
// swagger:parameters getSchedule updateSchedule deleteSchedule toggleSchedule
type schedIDPathParam struct {
	// The schedule ID.
	//
	// in: path
	// required: true
	SchedID int64 `json:"schedId"`
}

// varId path parameter.
// swagger:parameters getVariable updateVariable deleteVariable
type varIDPathParam struct {
	// The variable ID.
	//
	// in: path
	// required: true
	VarID int64 `json:"varId"`
}

// depId path parameter — kept for backward compatibility with the
// previously-documented /projects/{id}/deployments/{depId}/... URL
// family. The router no longer serves that family, so the parameter
// is not currently linked to any operation. Re-add the swagger:parameters
// line if/when a project-scoped route is mounted.
type depIDPathParam struct {
	// The deployment ID.
	//
	// in: path
	// required: true
	DepID int64 `json:"depId"`
}

// logId path parameter.
// swagger:parameters getLog
type logIDPathParam struct {
	// The log entry ID.
	//
	// in: path
	// required: true
	LogID int64 `json:"logId"`
}

// userId path parameter.
// swagger:parameters removeMember updateMemberRole
type userIDPathParam struct {
	// The user ID.
	//
	// in: path
	// required: true
	UserID int64 `json:"userId"`
}

// stageId path parameter.
// swagger:parameters updateLifecycleStage deleteLifecycleStage
type stageIDPathParam struct {
	// The lifecycle stage ID.
	//
	// in: path
	// required: true
	StageID int64 `json:"stageId"`
}

// tokenId path parameter.
// swagger:parameters revokeToken revokeAnyToken
type tokenIDPathParam struct {
	// The API token ID.
	//
	// in: path
	// required: true
	TokenID string `json:"id"`
}

// Body parameter structs.

// swagger:parameters createProject updateProject
type projectBodyParam struct {
	// in: body
	// required: true
	Body swaggerProjectRequest `json:"body"`
}

// swagger:parameters updateProjectNotifications
type projectNotificationBodyParam struct {
	// in: body
	// required: true
	Body swaggerProjectNotificationRequest `json:"body"`
}

// swagger:parameters createEnvironment updateEnvironment
type environmentBodyParam struct {
	// in: body
	// required: true
	Body swaggerEnvironmentRequest `json:"body"`
}

// swagger:parameters createLifecycle saveLifecycle
type lifecycleBodyParam struct {
	// in: body
	// required: true
	Body swaggerLifecycleRequest `json:"body"`
}

// swagger:parameters addLifecycleStage updateLifecycleStage
type lifecycleStageBodyParam struct {
	// in: body
	// required: true
	Body swaggerLifecycleStageRequest `json:"body"`
}

// swagger:parameters reorderLifecycleStages
type reorderStagesBodyParam struct {
	// in: body
	// required: true
	Body swaggerReorderStagesRequest `json:"body"`
}

// swagger:parameters createStep updateStep
type stepBodyParam struct {
	// in: body
	// required: true
	Body swaggerStepRequest `json:"body"`
}

// swagger:parameters reorderSteps
type reorderStepsBodyParam struct {
	// in: body
	// required: true
	Body swaggerReorderStepsRequest `json:"body"`
}

// swagger:parameters createTemplate updateTemplate
type stepTemplateBodyParam struct {
	// in: body
	// required: true
	Body swaggerStepTemplateRequest `json:"body"`
}

// swagger:parameters createVariable updateVariable
type variableBodyParam struct {
	// in: body
	// required: true
	Body swaggerVariableRequest `json:"body"`
}

// swagger:parameters createRelease
type releaseBodyParam struct {
	// in: body
	// required: true
	Body swaggerReleaseRequest `json:"body"`
}

// swagger:parameters createDeployment
type deploymentBodyParam struct {
	// in: body
	// required: true
	Body swaggerDeploymentScheduleRequest `json:"body"`
}

// swagger:parameters createSchedule updateSchedule
type scheduleBodyParam struct {
	// in: body
	// required: true
	Body swaggerScheduleRequest `json:"body"`
}

// swagger:parameters addMember
type projectMemberBodyParam struct {
	// in: body
	// required: true
	Body swaggerProjectMemberRequest `json:"body"`
}

// swagger:parameters updateMemberRole
type projectMemberRoleBodyParam struct {
	// in: body
	// required: true
	Body swaggerProjectMemberRoleRequest `json:"body"`
}

// swagger:parameters adminCreateUser adminUpdateUser
type userBodyParam struct {
	// in: body
	// required: true
	Body swaggerUserRequest `json:"body"`
}

// swagger:parameters createToken
type createTokenBodyParam struct {
	// in: body
	// required: true
	Body swaggerCreateTokenRequest `json:"body"`
}

// Query parameter structs.

// swagger:parameters listDeployments
type listDeploymentsQueryParam struct {
	// in: query
	ProjectID int64 `json:"project_id"`
	// in: query
	EnvID int64 `json:"env_id"`
	// in: query
	Status string `json:"status"`
	// in: query
	From int64 `json:"from"`
	// in: query
	To int64 `json:"to"`
	// in: query
	Limit int64 `json:"limit"`
	// in: query
	Offset int64 `json:"offset"`
}

// swagger:parameters streamLogs
type streamLogsQueryParam struct {
	// Format is either sse or ndjson.
	//
	// in: query
	Format string `json:"format"`
}

// swagger:parameters auditLog
type listAuditLogsQueryParam struct {
	// in: query
	Limit int64 `json:"limit"`
	// in: query
	UserID int64 `json:"user_id"`
	// in: query
	Action string `json:"action"`
	// in: query
	EntityType string `json:"entity_type"`
}

// swagger:parameters listTokens listAllTokens
type listTokensQueryParam struct {
	// in: query
	UserID int64 `json:"user_id"`
	// in: query
	Limit int64 `json:"limit"`
	// in: query
	Offset int64 `json:"offset"`
}

// swagger:parameters listVariables
type listVariablesQueryParam struct {
	// in: query
	EnvironmentID int64 `json:"environment_id"`
	// in: query
	SecretOnly bool `json:"secret_only"`
	// in: query
	Limit int64 `json:"limit"`
	// in: query
	Offset int64 `json:"offset"`
}

// swagger:parameters listReleases
type listReleasesQueryParam struct {
	// in: query
	Limit int64 `json:"limit"`
	// in: query
	Offset int64 `json:"offset"`
}

// swagger:parameters listProjects
type listProjectsQueryParam struct {
	// in: query
	Limit int64 `json:"limit"`
	// in: query
	Offset int64 `json:"offset"`
}

// swagger:parameters adminListUsers
type listUsersQueryParam struct {
	// in: query
	Limit int64 `json:"limit"`
	// in: query
	Offset int64 `json:"offset"`
}

// swagger:parameters listEnvironments
type listEnvironmentsQueryParam struct {
	// in: query
	Limit int64 `json:"limit"`
	// in: query
	Offset int64 `json:"offset"`
}

// swagger:parameters listLifecycles
type listLifecyclesQueryParam struct {
	// in: query
	Limit int64 `json:"limit"`
	// in: query
	Offset int64 `json:"offset"`
}

// swagger:parameters listSteps
type listStepsQueryParam struct {
	// in: query
	Limit int64 `json:"limit"`
	// in: query
	Offset int64 `json:"offset"`
}

// swagger:parameters listTemplates
type listTemplatesQueryParam struct {
	// in: query
	Limit int64 `json:"limit"`
	// in: query
	Offset int64 `json:"offset"`
}

// swagger:parameters listSchedules
type listSchedulesQueryParam struct {
	// in: query
	Limit int64 `json:"limit"`
	// in: query
	Offset int64 `json:"offset"`
}

// swagger:parameters listMembers
type listMembersQueryParam struct {
	// in: query
	Limit int64 `json:"limit"`
	// in: query
	Offset int64 `json:"offset"`
}

// swagger:parameters listDeploymentLogs
type listDeploymentLogsQueryParam struct {
	// in: query
	Limit int64 `json:"limit"`
	// in: query
	Offset int64 `json:"offset"`
}
