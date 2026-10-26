//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
	"time"

	"durpdeploy/internal/config"
)

func TestConfiguredStagingArchiveBrowserE2E(t *testing.T) {
	for _, limit := range []int64{4095, 4096} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			limits := config.StagingLimits{
				PayloadBytes: 24,
				Entries:      3,
				ArchiveBytes: limit,
			}
			f := newStagingLimitsFixture(t, limits)
			f.api(t, "POST", f.base()+"/steps", map[string]any{
				"name": "Archive boundary", "container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
				"approval_artifact_path": "plan", "approval_review_path": "review",
				"approval_review_format": "summary",
				"script_body": `set -eu
printf plan > "$DURPDEPLOY_STAGE_DIR/plan"
printf '{"create":0}' > "$DURPDEPLOY_STAGE_DIR/review"
printf 12345678 > "$DURPDEPLOY_STAGE_DIR/extra"`,
			}, 201)
			f.api(t, "POST", f.base()+"/steps", map[string]any{
				"name": "After context", "container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
				"script_body": "echo must-not-run",
			}, 201)
			release := verificationRelease(t, f, "archive-boundary")
			b := startPackageBrowser(t)
			b.setBackTestSession(t, f.baseURL, f.session)
			b.navigateBackTest(
				t,
				fmt.Sprintf(
					"%s/projects/%d/deploy?release_id=%d",
					f.baseURL,
					f.project.ID,
					release.ID,
				),
			)
			b.wait(
				t,
				`window.Alpine && window.htmx && document.querySelector('[name=environment_id]')`,
			)
			b.evaluate(t, fmt.Sprintf(`(() => {
document.addEventListener('htmx:afterRequest', event => {
 if (event.detail.xhr.status >= 400) window.archiveFormError = event.detail.xhr.responseText;
});
const field = document.querySelector('[name=environment_id]');
field.value = %q;
field.dispatchEvent(new Event('change', {bubbles:true}));
field.form.requestSubmit(); return true;
})()`, fmt.Sprint(f.environment.ID)))
			b.waitFor(
				t,
				`location.pathname.startsWith('/deployments/') || window.archiveFormError`,
				time.Minute,
			)
			if message := string(b.evaluate(t, `window.archiveFormError || ''`)); message != `""` {
				t.Fatalf("deployment form failed: %s", message)
			}
			d := latestNotesDeployment(t, f)
			want := "failed"
			if limit == 4096 {
				want = "awaiting_artifact_approval"
			}
			waitVerificationStatus(t, f, d.ID, want)
			b.navigateBackTest(
				t,
				fmt.Sprintf("%s/deployments/%d", f.baseURL, d.ID),
			)
			b.wait(
				t,
				fmt.Sprintf(`document.body.textContent.includes(%q)`, want),
			)
			b.captureNavigation(t, fmt.Sprintf("staging-archive-%d", limit))
			if limit == 4096 {
				f.api(
					t,
					"POST",
					fmt.Sprintf("/api/v1/deployments/%d/cancel", d.ID),
					nil,
					200,
				)
				waitVerificationStatus(t, f, d.ID, "cancelled")
			}
			assertStagingPolicyCleanup(t, f)
		})
	}
}
