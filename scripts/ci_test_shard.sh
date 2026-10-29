#!/usr/bin/env bash
# Run one isolated test shard without changing its original checks.
set -euo pipefail
tests=()
case ${1:?usage: ci_test_shard.sh SUITE} in
  rollout-contracts)
    make agent-rollout-gate
    bash scripts/run_named_go_tests.sh --race --tags e2e --timeout 3m \
      --packages ./internal/handler/api \
      --tests TestConcurrentLogDisconnectAPIWebE2E \
      --require TestConcurrentLogDisconnectAPIWebE2E
    bash scripts/go_test.sh -race -count=1 ./internal/artifact
    bash scripts/go_test.sh -tags=e2e -race -count=1 -timeout=5m -run '^Test(Project(UpdateAtomic|PanelScope|PanelFailure|PanelBatch)|StepExecutionLimits|ConfiguredExecutionLimits|StepFields|RouteAuditAuthorization|TerminalRetryShutdown|FramingPolicy|TokenFormat|JSONNotificationDelivery|UserRoleValidation|TextLogExport)APIWebE2E$' ./internal/handler/api
    bash scripts/go_test.sh -tags=e2e -race -count=1 -timeout=5m -run '^TestProxyBodyAPIWebE2E$' ./internal/handler/api
    bash scripts/go_test.sh -tags=e2e -race -count=1 -timeout=5m -run '^TestConfigured(RequestBody(APIWeb|Isolation)|PasswordCompatibility|ArtifactTimeoutsAPIWeb|ArtifactDownload(APIWeb|RemoteAPI)|ArtifactSafety(APIWeb|RemoteAPI))E2E$' ./internal/handler/api
    bash scripts/go_test.sh -tags=e2e -race -count=1 -timeout=5m -run '^Test(UserUpdateAtomic|RoleChangeRevocation|ReleaseCaptureConsistent|LifecycleStageOrder)APIWebE2E$' ./internal/handler
    bash scripts/run_named_go_tests.sh --race --tags e2e --timeout 5m \
      --packages ./internal/handler/api \
      --tests 'TestLifecycleReorderApprovalAPIWebE2E|TestLifecycleReorderAccessAPIWebE2E' \
      --require 'TestLifecycleReorderApprovalAPIWebE2E|TestLifecycleReorderAccessAPIWebE2E'
    bash scripts/go_test.sh -tags=e2e -race -count=1 -timeout=5m -run '^TestLocalAdministrationAPIWebE2E$' ./internal/handler
    bash scripts/go_test.sh -tags=e2e -race -count=1 -timeout=3m -run '^Test(PermissionGuidance(Deployment)?|UsersPhone|UsersDefaultRole|UsersFormAccessibility)APIWebE2E$' ./internal/handler/api
    (
    export DURPDEPLOY_CONTAINER_RUNTIME=docker
    bash scripts/go_test.sh -tags=e2e -race -count=1 -timeout=5m -run '^TestConfiguredStaging(CaptureAPIWeb|CapacityAPI|RemoteAPI|ApprovalResumeAPI)E2E$' ./internal/handler/api
    bash scripts/go_test.sh -tags=e2e -race -count=1 -timeout=5m -run '^TestConfigured(LogsAPIWeb|PendingLogsAPI)E2E$' ./internal/handler/api
    bash scripts/go_test.sh -tags=e2e -count=1 -run '^(TestConfiguredContainer(PIDs|Resources|ResourcesRemoteRejection)APIE2E|TestConfiguredPIDRemoteRejectionAPIE2E|TestConfiguredArtifact(SafetyContainer|ApprovalNodes|ApprovalResume)APIE2E|TestConfiguredDefaultTimeout(API|RemoteAPI)E2E)$' ./internal/handler/api
    )
    (
    export DURPDEPLOY_CONTAINER_RUNTIME=docker
    bash scripts/go_test.sh -tags=e2e -race -count=1 -timeout=5m -run '^TestEnvironmentQueue.*E2E$' ./internal/handler/api
    )
    ;;
  rollout-artifacts-1|rollout-artifacts-2)
    (
    export DURPDEPLOY_CONTAINER_RUNTIME=docker
    tests=$(bash scripts/ci_artifact_tests.sh "${1##*-}")
    required=${tests//$'\n'/|}
    bash scripts/run_named_go_tests.sh --race --tags e2e --timeout 20m \
      --packages ./internal/handler/api --tests "$required" --require "$required"
    )
    ;;
  rollout-verification)
    (
    export DURPDEPLOY_CONTAINER_RUNTIME=docker
    bash scripts/go_test.sh -tags=e2e -race -count=1 -timeout=20m -run '^Test(Verification|Rollback).*E2E$' ./internal/handler/api
    )
    ;;
  browser-navigation)
    tests=(
      '^TestConfiguredRequestBodyBrowserE2E$'
      '^TestConfiguredArtifactDownloadBrowserE2E$'
      '^TestConfiguredArtifactSafetyBrowserE2E$'
      '^TestConfiguredDefaultTimeoutBrowserE2E$'
      '^TestConfiguredExecutionLimitsBrowserE2E$'
      '^TestConfiguredExecutionPrecisionBrowserE2E$'
      '^TestConfiguredStagingArchiveBrowserE2E$'
      '^TestSelfPasswordChange'
      '^TestStaleFormBackBrowserE2E$'
      '^TestSwaggerTokensBrowserE2E$'
      '^TestLoginDestination(APIWeb|HTMXAPIWeb|Browser)E2E$'
      '^TestLoginCrossOrigin(APIWeb|Browser)E2E$'
      '^TestDeploymentNotes(APIWeb|Browser)E2E$'
      '^TestDeploymentFilterEmptyAPIWebE2E$'
      '^TestBrowserContextIsolationE2E$'
      '^TestScreenshotRenderingReadinessBrowserE2E$'
      '^Test(SwaggerInstance|ReleaseDeferredPackage|ReleaseRefreshConflictStaysOnPage|ReleaseCapture|ThemeStyles|PackageRepository|PackageRepositoryBack|BackNavigation|BackHistory|PageNavigation|PageNavigationExpiredSession|OIDCNavigation|FramingPolicy)BrowserE2E$'
      '^Test(DeploymentMobile|DeploymentListMobile|ResourceListMobile|HomeMobile|HomeRunningPoll|HomeChartTimer|SchedulePages|ScheduleTransitions|MainPagePattern)BrowserE2E$'
      '^Test(LifecycleVariables|LifecycleLiveScope|LifecyclePageNavigation|LifecycleStageOrder|VariableScopesBeforeDeployment|NotificationCredentialForms|NotificationCardsViewer|NotificationValidationPreservesForm|SMTPPlaintextWarning)BrowserE2E$'
    )
    ;;
  browser-resources)
    tests=(
      '^TestTemplateLibrary.*E2E$'
      '^TestStepVariables(APIWeb|Browser)E2E$'
      '^TestReleaseProvenance.*E2E$'
      '^TestProjectGroups.*E2E$'
      '^TestProjectTemplates.*(APIWeb|Browser)E2E$'
      '^TestPromotionGuidance(APIWeb|Browser)E2E$'
      '^TestRunbookRetryVersion(APIWeb|Browser)E2E$'
      '^TestRunbookHistoryPhone(APIWeb|Browser)E2E$'
      '^TestReleaseContent(APIWeb|Browser)E2E$'
      '^TestVariable(Names(APIWeb|Browser)|CancelBrowser)E2E$'
      '^TestReleaseDuplicateRecovery(APIWeb|Browser)E2E$'
      '^TestReleaseVariableGuidance(APIWeb|Browser)E2E$'
      '^TestRerunInputs(APIWeb|Browser)E2E$'
      '^TestStepFormActionsBrowserE2E$'
      '^TestAuditIdentities(APIWeb|Browser)E2E$'
    )
    ;;
  browser-permissions)
    tests=(
      '^Test(APITokenTelemetry|UserUpdateAtomic|LastAdministrator|RoleChangeRevocation|ProjectPanelFailure|StepExecutionLimits|StepFields|PermissionGuidance(Deployment)?|UsersFormAccessibility|UsersDefaultRole|UsersPhone|ProjectStaleSave|ProjectMemberRoles|UserModalPermissionFeedback|MemberModalErrorFeedback|ProjectPanelScope|ProjectEditModal|ResourceCreateModal|ResourceEditModal|StepEditModal|StepAddModal|MobileFormFocus|RunbookEditor|RunbookDeletion|AlpineEditForms|AlpineDialogs|RunningServer|ProjectTokens|RolePermissions)BrowserE2E$'
      '^TestSecretExport.*E2E$'
      '^TestRunbookDeletion.*APIWebE2E$'
      '^Test(ProjectTokens.*|PermissionRolesExecution.*)(API|APIWeb)E2E$'
      '^Test((Deployment|Runbook)LogHistoryPages|OpenLogStreamRevocation|StalledSMTPDelivery|SMTPPlaintextWarning|BackupHealthRecovery|NotificationDiagnostics)APIWebE2E$'
    )
    ;;
  browser-execution)
    tests=(
      '^TestConfiguredLogsBrowserE2E$'
      '^TestLogFollowingBrowserE2E$'
      '^TestQueuedCancellation(APIWeb|Browser)E2E$'
      '^Test(DeploymentStepLogs|DeploymentStepPanels|DeploymentStepLogsHistoricalQuiet|DeploymentLogHistory|RunbookLogHistory|TextLogExport|RunbookLogRevocation|DeploymentStagingFailure|LegacyRunbookStreamDrainsAfterTerminalBadge)BrowserE2E$'
      '^TestVerification(Rollback|Polling|CleanupRetry)BrowserE2E$'
      '^TestEnvironmentQueueBrowserE2E$'
      '^Test(ArtifactGate.*|TerraformPlanColors)BrowserE2E$'
    )
    ;;
  browser-agents)
    bash scripts/go_test.sh -tags=e2e,agentbrowser -race -count=1 -timeout=10m -v -run '^(TestBuildInfoBrowserE2E|TestAgentFleetBrowserE2E|TestAgentDeleteBrowserE2E|TestDeploymentWaitingForAgentsBrowserE2E)$' ./internal/handler/api
    bash scripts/go_test.sh -tags=e2e,agentbrowser -race -count=1 -timeout=10m -v -run '^TestAgentContainerBrowserE2E$' ./internal/handler/api
    for version in v1.2.3+build.4 v0.0.0-dev.742+g0123456789ab; do
      bash scripts/go_test.sh -tags=e2e,agentbrowser -race -count=1 -timeout=10m \
        -ldflags "-X durpdeploy/internal/buildinfo.version=$version -X durpdeploy/internal/buildinfo.buildNumber=742 -X durpdeploy/internal/buildinfo.commit=$CI_COMMIT_SHA -X durpdeploy/internal/buildinfo.modified=false" \
        -run '^TestBuildInfo(APIWeb|Browser)E2E$' ./internal/handler/api
    done
    ;;
  *) echo "Unknown CI test shard: $1" >&2; exit 2 ;;
esac

if ((${#tests[@]})); then
  selected=$(IFS='|'; printf '%s' "${tests[*]}")
  bash scripts/go_test.sh -tags=e2e,packagebrowser -race -count=1 \
    -timeout=20m -v -run "$selected" ./internal/handler/api
fi
