//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
)

func TestDeploymentStagingFailureBrowserE2E(t *testing.T) {
	f, deployment := stagingFailureE2E(t)
	b := startPackageBrowser(t)
	b.setStepLogSession(t, f.baseURL, f.session)
	b.openStepLogPage(
		t,
		fmt.Sprintf("%s/deployments/%d", f.baseURL, deployment.ID),
	)
	b.wait(
		t,
		`document.querySelector('[data-step-index="0"] .badge').textContent === 'Failed' && Alpine.$data(document.querySelector('[x-data^="deploymentStepLogs"]')).source === null`,
	)
	b.captureNavigation(t, "staging-failed")
}
