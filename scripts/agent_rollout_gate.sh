#!/usr/bin/env bash
set -euo pipefail

ROOT=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)

tests=(
	TestRemoteStepResultCompletesRun
	TestRemoteLogsOrderBySequence
	TestRemoteResultCompletesOnce
	TestRemoteStepFanoutTargetsEveryMatchingEnvironmentAgent
	TestRemoteStepCancellationAcknowledgement
	TestMaintainMarksStaleRemoteStepCancellationUnconfirmed
	TestMaintainMarksRemoteStepLostWhenHeartbeatStale
	TestRemoteLifecycleSurvivesRepositoryRestart
	TestRecoverPendingDeploymentsCancelsActiveRemoteStepBeforeFailure
	TestRemoteStepStartRejectsExpiredClaim
	TestRemoteStepStartRequiresLiveEligibleClaim
	TestDispatchDatabaseParity
	TestRemoteStepFanoutReturnsNoWorkWithoutMatchingAgent
	TestStartMaintenanceExactDeadlineAcrossDatabases
	TestRemoteLogAndTerminalRevocationAcrossDatabases
)
required=$(IFS='|'; printf '%s' "${tests[*]}")

exec bash "$ROOT/scripts/run_named_go_tests.sh" --race \
	--packages \
	"./cmd/server ./internal/agentserver ./internal/dispatch ./internal/repository" \
	--tests "$required" --require "$required"
