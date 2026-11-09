//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"

	"durpdeploy/internal/repository"
)

func TestStartupRecoveryDiagnosticsBrowserE2E(t *testing.T) {
	f, d := recoveredDeploymentFixture(t)
	b := startPackageBrowser(t)
	b.setStepLogSession(t, f.baseURL, f.session)
	b.openStepLogPage(t, fmt.Sprintf("%s/deployments/%d", f.baseURL, d.ID))
	b.wait(
		t,
		fmt.Sprintf(
			`document.body.textContent.includes(%q) && document.body.textContent.includes('Operator note')`,
			repository.RecoveryCleanupConfirmed,
		),
	)
	b.captureStepLogs(t, "startup-recovery")
}
