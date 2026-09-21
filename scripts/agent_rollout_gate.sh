#!/usr/bin/env bash
set -euo pipefail

ROOT=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)

bash "$ROOT/scripts/check-agent-compose-contract.sh"
bash "$ROOT/scripts/check-agent-helm-contract.sh"
if grep -Fq 'remote_deployment_claims' "$ROOT/scripts/agent_lifecycle_faults.mjs"; then
	printf '%s\n' 'agent rollout: lifecycle faults use retired deployment claims' >&2
	exit 1
fi
grep -Fq 'FROM remote_step_runs' "$ROOT/scripts/agent_lifecycle_faults.mjs"

tests=(
	TestRemoteStepResultCompletesRun
	TestRemoteLogsOrderBySequence
	TestRemoteResultCompletesOnce
	TestRemoteStepFanoutTargetsEveryMatchingEnvironmentAgent
	TestRemoteStepFanoutRequiresSnapshotInterpreterCapability
	TestRemoteStepClaimRechecksInterpreterCapability
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
	TestPollReplacesInterpreterCapabilitiesByProtocol
	TestRemoteStepPayloadIncludesNonBashInterpreter
	TestAgentPollCapabilityReplacementIsTransactional
	TestRunner_FailsWhenNoAgentSupportsInterpreter
)
required=$(IFS='|'; printf '%s' "${tests[*]}")

exec bash "$ROOT/scripts/run_named_go_tests.sh" --race \
	--packages \
	"./cmd/server ./internal/agentserver ./internal/dispatch ./internal/repository ./internal/runner" \
	--tests "$required" --require "$required"
