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
    bash scripts/go_test.sh -tags=e2e -race -count=1 -timeout=3m -run '^Test(PermissionGuidance(Deployment)?|ProjectPanelScope|UsersPhone|UsersDefaultRole|UsersFormAccessibility)APIWebE2E$' ./internal/handler/api
    bash scripts/go_test.sh -tags=e2e -race -count=1 -timeout=5m -run '^TestLifecycleStageOrderAPIWebE2E$' ./internal/handler
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
      '^TestSelfPasswordChange'
      '^TestStaleFormBackBrowserE2E$'
      '^TestSwaggerTokensBrowserE2E$'
      '^TestLoginDestination(APIWeb|HTMXAPIWeb|Browser)E2E$'
      '^TestDeploymentNotes(APIWeb|Browser)E2E$'
      '^TestDeploymentFilterEmptyAPIWebE2E$'
      '^TestBrowserContextIsolationE2E$'
      '^TestScreenshotRenderingReadinessBrowserE2E$'
      '^Test(SwaggerInstance|ReleaseDeferredPackage|ReleaseRefreshConflictStaysOnPage|ThemeStyles|PackageRepository|PackageRepositoryBack|BackNavigation|BackHistory|PageNavigation|PageNavigationExpiredSession|OIDCNavigation)BrowserE2E$'
      '^Test(DeploymentMobile|DeploymentListMobile|ResourceListMobile|HomeMobile|HomeRunningPoll|HomeChartTimer|SchedulePages|MainPagePattern)BrowserE2E$'
      '^Test(LifecycleVariables|LifecycleLiveScope|LifecyclePageNavigation|LifecycleStageOrder|VariableScopesBeforeDeployment|NotificationCredentialForms|NotificationValidationPreservesForm|SMTPPlaintextWarning)BrowserE2E$'
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
      '^TestSecretExport.*E2E$'
      '^Test(PermissionGuidance(Deployment)?|UsersFormAccessibility|UsersDefaultRole|UsersPhone|ProjectStaleSave|ProjectMemberRoles|UserModalPermissionFeedback|MemberModalErrorFeedback|ProjectPanelScope|ProjectEditModal|ResourceCreateModal|ResourceEditModal|StepEditModal|StepAddModal|MobileFormFocus|RunbookEditor|RunbookDeletion|AlpineEditForms|AlpineDialogs|RunningServer|ProjectTokens|RolePermissions)BrowserE2E$'
      '^TestRunbookDeletion.*APIWebE2E$'
      '^Test(ProjectTokens.*|PermissionRolesExecution.*)(API|APIWeb)E2E$'
      '^Test((Deployment|Runbook)LogHistoryPages|OpenLogStreamRevocation|StalledSMTPDelivery|SMTPPlaintextWarning|BackupHealthRecovery)APIWebE2E$'
    )
    ;;
  browser-execution)
    tests=(
      '^TestLogFollowingBrowserE2E$'
      '^TestQueuedCancellation(APIWeb|Browser)E2E$'
      '^Test(DeploymentStepLogs|DeploymentStepPanels|DeploymentStepLogsHistoricalQuiet|DeploymentLogHistory|RunbookLogHistory|RunbookLogRevocation|DeploymentStagingFailure|LegacyRunbookStreamDrainsAfterTerminalBadge)BrowserE2E$'
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
