//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
)

func TestPromotionGuidanceBrowserE2E(t *testing.T) {
	fixture := newPromotionGuidance(t)
	fixture.forceProd(t)
	f := fixture.f
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, fixture.operator.session)
	path := fmt.Sprintf(
		"/projects/%d/deploy?release_id=%d",
		f.project.ID,
		fixture.release.ID,
	)
	for next, env := range fixture.environments {
		for _, width := range []int{375, 390, 768, 1280} {
			b.call(
				t,
				"Emulation.setDeviceMetricsOverride",
				map[string]any{
					"width":             width,
					"height":            900,
					"deviceScaleFactor": 1,
					"mobile":            false,
				},
				&struct{}{},
			)
			b.navigateBackTest(t, f.baseURL+path)
			b.wait(
				t,
				`window.Alpine && document.querySelector('#deploy-environment')`,
			)
			assertPromotionGuidance(t, b, fixture, next)
			b.screenshot(t, fmt.Sprintf("promotion-stage%d-%d", next, width))
		}
		b.evaluate(
			t,
			fmt.Sprintf(
				`(() => {const select=document.querySelector('#deploy-environment');select.value='%d';select.dispatchEvent(new Event('change',{bubbles:true}));select.form.querySelector('[type="submit"]').click();return true;})()`,
				env.ID,
			),
		)
		b.wait(t, `location.pathname.startsWith('/deployments/')`)
		var page struct{ Items []db.Deployment }
		decodeStepLogTest(
			t,
			f.api(t, "GET", f.base()+"/deployments", nil, 200),
			&page,
		)
		if len(page.Items) == 0 || page.Items[0].EnvironmentID != env.ID {
			t.Fatal("Browser promotion selected the wrong environment")
		}
		f.completion(t, page.Items[0].ID, events.DeploymentSucceeded)
	}
}

func assertPromotionGuidance(
	t *testing.T,
	b *packageBrowser,
	fixture promotionGuidanceFixture,
	next int,
) {
	t.Helper()
	for index, env := range fixture.environments {
		reason := ""
		if index > 0 {
			reason = fmt.Sprintf(
				"Deploy %s successfully to %s first.",
				fixture.release.Version,
				fixture.environments[index-1].Name,
			)
		}
		check := fmt.Sprintf(`(() => {
 const select=document.querySelector('#deploy-environment');
 const option=select.querySelector('option[value="%d"]');
 const item=document.querySelector('[data-promotion-environment-id="%d"]');
 const blocked=%t;
 if (!option || !!item!==blocked || (blocked && (!option.disabled || item.innerText!==%q))) return false;
 if (blocked && (item.scrollWidth>item.clientWidth || !item.getClientRects().length || select.getAttribute('aria-describedby')!=='promotion-guidance')) return false;
 return document.documentElement.scrollWidth<=innerWidth;
})()`, env.ID, env.ID, index > next, env.Name+": "+reason)
		b.wait(t, check)
	}
}
