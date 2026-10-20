#!/usr/bin/env bash
# Run one isolated test shard without changing its original checks.
set -euo pipefail
case ${1:?usage: ci_test_shard.sh SUITE} in
  rollout-contracts)
    make agent-rollout-gate
    bash scripts/go_test.sh -race -count=1 ./internal/artifact
    bash scripts/go_test.sh -tags=e2e -race -count=1 -timeout=3m -run '^Test(PermissionGuidance(Deployment)?|ProjectPanelScope|UsersPhone|UsersDefaultRole|UsersFormAccessibility)APIWebE2E$' ./internal/handler/api
    (
    export DURPDEPLOY_CONTAINER_RUNTIME=docker
    bash scripts/go_test.sh -tags=e2e -count=1 -run '^(TestReleaseDeferredPackageAPIWebE2E|TestArtifactsAPIWebContainerE2E|TestArtifactCredentialRotationE2E|TestArtifactSourceReplacementE2E|TestDeploymentStagingCleanupE2E|TestDeploymentStepResourcesE2E|TestDeploymentStagingFailureAPIWebE2E|TestDeploymentStepLogsAPIWebE2E|TestDeploymentStepLogOutcomesAPIWebE2E|TestPageNavigationHTTPAPIWebE2E|TestLifecycleVariablesAPIWebE2E|TestLifecycleVariableScopeChangesApplyToExistingReleaseE2E)$' ./internal/handler/api
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
    bash scripts/go_test.sh -tags=e2e,packagebrowser -race -count=1 -timeout=10m -run '^TestBrowserContextIsolationE2E$' ./internal/handler/api
    bash scripts/go_test.sh -tags=e2e,packagebrowser -race -count=1 -timeout=10m -v -run '^Test(SwaggerInstance|ReleaseDeferredPackage|ReleaseRefreshConflictStaysOnPage|ThemeStyles|PackageRepository|PackageRepositoryBack|BackNavigation|BackHistory|PageNavigation|PageNavigationExpiredSession|OIDCNavigation)BrowserE2E$' ./internal/handler/api
    bash scripts/go_test.sh -tags=e2e,packagebrowser -race -count=1 -timeout=10m -v -run '^Test(DeploymentMobile|DeploymentListMobile|ResourceListMobile|HomeMobile|HomeRunningPoll|HomeChartTimer|SchedulePages|MainPagePattern)BrowserE2E$' ./internal/handler/api
    bash scripts/go_test.sh -tags=e2e,packagebrowser -race -count=1 -timeout=10m -v -run '^Test(LifecycleVariables|LifecycleLiveScope|LifecyclePageNavigation|VariableScopesBeforeDeployment|NotificationCredentialForms|NotificationValidationPreservesForm|SMTPPlaintextWarning)BrowserE2E$' ./internal/handler/api
    ;;
  browser-resources)
    bash scripts/go_test.sh -tags=e2e,packagebrowser -race -count=1 -timeout=3m -v -run '^TestReleaseContent(APIWeb|Browser)E2E$' ./internal/handler/api
    bash scripts/go_test.sh -tags=e2e,packagebrowser -race -count=1 -timeout=3m -v -run '^TestVariableNames(APIWeb|Browser)E2E$' ./internal/handler/api
    bash scripts/go_test.sh -tags=e2e,packagebrowser -race -count=1 -timeout=3m -v -run '^TestReleaseDuplicateRecovery(APIWeb|Browser)E2E$' ./internal/handler/api
    bash scripts/go_test.sh -tags=e2e,packagebrowser -race -count=1 -timeout=3m -v -run '^TestReleaseVariableGuidance(APIWeb|Browser)E2E$' ./internal/handler/api
    bash scripts/go_test.sh -tags=e2e,packagebrowser -race -count=1 -timeout=3m -v -run '^TestRerunInputs(APIWeb|Browser)E2E$' ./internal/handler/api
    bash scripts/go_test.sh -tags=e2e,packagebrowser -race -count=1 -timeout=3m -v -run '^TestStepFormActionsBrowserE2E$' ./internal/handler/api
    bash scripts/go_test.sh -tags=e2e,packagebrowser -race -count=1 -timeout=3m -v -run '^TestAuditIdentities(APIWeb|Browser)E2E$' ./internal/handler/api
    bash scripts/go_test.sh -tags=e2e,packagebrowser -race -count=1 -timeout=10m -v -run '^Test(PermissionGuidance(Deployment)?|UsersFormAccessibility|UsersDefaultRole|UsersPhone|ProjectStaleSave|ProjectMemberRoles|ProjectPanelScope|ProjectEditModal|ResourceCreateModal|ResourceEditModal|StepEditModal|StepAddModal|MobileFormFocus|RunbookEditor|RunbookDeletion|AlpineEditForms|AlpineDialogs|RunningServer|ProjectTokens|RolePermissions)BrowserE2E$' ./internal/handler/api
    bash scripts/go_test.sh -tags=e2e -race -count=1 -timeout=10m -v -run '^TestRunbookDeletion.*APIWebE2E$' ./internal/handler/api
    bash scripts/go_test.sh -tags=e2e -race -count=1 -timeout=10m -v -run '^Test(ProjectTokens.*|PermissionRolesExecution.*)APIE2E$' ./internal/handler/api
    bash scripts/go_test.sh -tags=e2e -race -count=1 -timeout=3m -run '^Test((Deployment|Runbook)LogHistoryPages|OpenLogStreamRevocation|StalledSMTPDelivery|SMTPPlaintextWarning|BackupHealthRecovery)APIWebE2E$' ./internal/handler/api
    ;;
  browser-execution)
    bash scripts/go_test.sh -tags=e2e,packagebrowser -race -count=1 -timeout=3m -v -run '^TestQueuedCancellation(APIWeb|Browser)E2E$' ./internal/handler/api
    bash scripts/go_test.sh -tags=e2e,packagebrowser -race -count=1 -timeout=10m -v -run '^Test(DeploymentStepLogs|DeploymentStepPanels|DeploymentStepLogsHistoricalQuiet|DeploymentLogHistory|RunbookLogHistory|RunbookLogRevocation|DeploymentStagingFailure|LegacyRunbookStreamDrainsAfterTerminalBadge)BrowserE2E$' ./internal/handler/api
    bash scripts/go_test.sh -tags=e2e,packagebrowser -race -count=1 -timeout=10m -v -run '^TestVerification(Rollback|Polling|CleanupRetry)BrowserE2E$' ./internal/handler/api
    bash scripts/go_test.sh -tags=e2e,packagebrowser -race -count=1 -timeout=10m -v -run '^TestEnvironmentQueueBrowserE2E$' ./internal/handler/api
    bash scripts/go_test.sh -tags=e2e,packagebrowser -race -count=1 -timeout=10m -v -run '^Test(ArtifactGate.*|TerraformPlanColors)BrowserE2E$' ./internal/handler/api
    ;;
  browser-agents)
    (
    export DURPDEPLOY_E2E_RUNNING_SUITE=1
    export DURPDEPLOY_E2E_PORT=18087
    bash scripts/e2e_test.sh
    )
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
