//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/events"
)

func TestTerraformPlanColorsBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	f.api(t, "POST", f.base()+"/steps", map[string]any{
		"name":                   "Terraform plan",
		"container_image":        "docker.io/library/bash:5.2",
		"approval_artifact_path": "tfplan",
		"approval_review_path":   "review.json",
		"approval_review_format": "terraform",
		"script_body": `sleep 3
printf '\033[1;32m+ added\033[0m neutral\n\033[31m- removed\033[0m\n\033[33m~ modified\033[0m\n'
printf '  + plain-add\n  - plain-remove\n  ~ plain-modify\n  -/+ replacement\n'
printf '\033[32m<img src=x onerror=alert(1)>\033[0m\n'
printf plan > "$DURPDEPLOY_STAGE_DIR/tfplan"
printf '%s' '{"format_version":"1.2","resource_changes":[{"address":"example.add","change":{"actions":["create"],"after":{"message":"<img src=x>","password":"hidden-value"},"after_sensitive":{"password":true}}},{"address":"example.remove","change":{"actions":["delete"],"before":{"message":"old"},"before_sensitive":false}},{"address":"example.modify","change":{"actions":["update"],"before":{"message":"old"},"after":{"message":"new"},"before_sensitive":false,"after_sensitive":false}}]}' > "$DURPDEPLOY_STAGE_DIR/review.json"`,
	}, 201)
	f.api(t, "POST", f.base()+"/steps", map[string]string{
		"name": "Terraform apply", "script_body": "true",
		"container_image": "docker.io/library/bash:5.2",
	}, 201)
	release := verificationRelease(t, f, "plan-colors")
	b := startPackageBrowser(t)
	b.setStepLogSession(t, f.baseURL, f.session)
	deployment := verificationDeploy(t, f, release)
	address := fmt.Sprintf("%s/deployments/%d", f.baseURL, deployment.ID)
	b.openStepLogPage(t, address)
	const colors = `(() => {
 const logs = document.querySelector('[data-step-index="0"]');
 const has = (cls, text) => [...logs.querySelectorAll('.'+cls)].some(s => s.textContent.includes(text));
 return has('text-success','+ added') && has('text-error','- removed') &&
 has('text-warning','~ modified') && has('text-success','plain-add') &&
 has('text-error','plain-remove') && has('text-warning','plain-modify') &&
 has('text-warning','replacement') && has('font-bold','+ added') &&
 !logs.querySelector('img') && !logs.textContent.includes('\u001b') &&
 [...logs.querySelectorAll('span')].some(s => s.textContent === ' neutral' && !s.className);
})()`
	b.wait(t, colors)
	if string(
		b.evaluate(
			t,
			`Alpine.$data(document.querySelector('[x-data^="deploymentStepLogs"]')).panels[0].live.length > 0`,
		),
	) != "true" {
		t.Fatal("color check did not exercise live streamed output")
	}
	f.completion(t, deployment.ID, events.ArtifactAwaitingApproval)
	logs := f.api(t, "GET", fmt.Sprintf(
		"/api/v1/deployments/%d/logs", deployment.ID,
	), nil, 200)
	if !strings.Contains(string(logs), `\u001b[1;32m+ added`) {
		t.Fatal("public log API lost the original terminal colors")
	}
	b.navigateBackTest(t, address)
	b.wait(t, colors)
	var review artifact.TerraformReviewResponse
	decodeStepLogTest(
		t,
		f.api(t, "GET", gateAPIPath(deployment.ID)+"/0/review", nil, 200),
		&review,
	)
	if len(review.Resources) != 3 ||
		!strings.Contains(review.Resources[0].After, "[REDACTED]") ||
		strings.Contains(review.Resources[0].After, "hidden-value") {
		t.Fatal(
			"public review contract lost changes or exposed sensitive values",
		)
	}
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
		`document.querySelector('[data-terraform-plan] .badge-success')?.textContent === '+ Add' && document.querySelector('[data-terraform-plan] .badge-error')?.textContent === '− Remove' && document.querySelector('[data-terraform-plan] .badge-warning')?.textContent === '~ Modify'`,
	)
	b.captureNavigation(t, "terraform-plan-colors", func() {
		b.wait(
			t,
			`document.querySelectorAll('[data-terraform-plan] .border-error').length === 3 && document.querySelectorAll('[data-terraform-plan] .border-success').length === 3 && !document.querySelector('[data-terraform-plan] img') && !document.querySelector('[data-terraform-plan]').textContent.includes('hidden-value')`,
		)
		b.evaluate(
			t,
			`document.querySelector('[data-terraform-plan]').scrollIntoView(); true`,
		)
	})
	b.captureNavigation(t, "terraform-colored-logs", func() {
		b.evaluate(
			t,
			`(() => { const logs = document.querySelector('[data-step-index="0"]'); logs.open = true; logs.scrollIntoView(); return true; })()`,
		)
		b.wait(t, colors)
	})
}
