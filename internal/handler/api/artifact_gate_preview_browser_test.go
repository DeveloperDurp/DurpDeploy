//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"

	"durpdeploy/internal/events"
)

func TestArtifactGatePreviewOmissionBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	f.api(t, "POST", f.base()+"/steps", map[string]string{
		"name": "Generate", "container_image": "docker.io/library/bash:5.2",
		"approval_artifact_path": "tfplan", "approval_review_path": "review.json",
		"approval_review_format": "terraform", "script_body": oversizedPreviewScript,
	}, 201)
	f.api(t, "POST", f.base()+"/steps", map[string]string{
		"name": "Apply", "container_image": "docker.io/library/bash:5.2", "script_body": "true",
	}, 201)
	deployment := verificationDeploy(
		t,
		f,
		verificationRelease(t, f, "preview-omission-browser"),
	)
	f.completion(t, deployment.ID, events.ArtifactAwaitingApproval)
	b := startPackageBrowser(t)
	b.setStepLogSession(t, f.baseURL, f.session)
	b.openStepLogPage(
		t,
		fmt.Sprintf("%s/deployments/%d", f.baseURL, deployment.ID),
	)
	b.wait(t, `document.querySelector('[data-terraform-review]') !== null`)
	b.evaluate(t, `new Promise(resolve => {
 document.body.addEventListener('htmx:afterSettle', function settled(event) {
  if (!event.target.matches('[data-artifact-gates]')) return;
  document.body.removeEventListener('htmx:afterSettle', settled);
  resolve(true);
 });
})`)
	b.evaluate(
		t,
		`document.querySelector('[data-terraform-review]').open = true; true`,
	)
	b.wait(
		t,
		`document.querySelector('[data-terraform-preview-truncated]')?.getAttribute('role') === 'status' && document.querySelector('[data-terraform-plan]').textContent.includes('[OMITTED FROM PREVIEW]')`,
	)
	b.captureNavigation(t, "terraform-preview-omission", func() {
		b.wait(
			t,
			`document.querySelectorAll('[data-terraform-plan] section').length === 100 && !document.querySelector('[data-terraform-plan]').textContent.includes('private-review-content')`,
		)
		b.evaluate(
			t,
			`document.querySelector('[data-terraform-plan]').scrollIntoView(); true`,
		)
	})
}
