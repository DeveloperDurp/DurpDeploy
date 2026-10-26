//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"durpdeploy/internal/config"
	"github.com/google/uuid"
)

func TestConfiguredRequestBodyBrowserE2E(t *testing.T) {
	base := strings.TrimRight(os.Getenv("DURPDEPLOY_LIVE_BASE"), "/")
	session := os.Getenv("DURPDEPLOY_LIVE_SESSION")
	token := os.Getenv("DURPDEPLOY_LIVE_API_TOKEN")
	if base == "" {
		limits := config.Defaults().Limits.HTTP
		limits.FormBytes = 2048
		limits.PasswordFormBytes, limits.RepositoryFormBytes = 512, 768
		f := newConfiguredBodyFixture(t, limits)
		base, session, token = f.baseURL, f.session, f.token
	}
	if session == "" || token == "" {
		t.Fatal(
			"configured body browser test requires its session and API token",
		)
	}
	b := startPackageBrowser(t)
	b.setBackTestSession(t, base, session)
	b.navigateBackTest(t, base+"/projects/new")
	b.wait(
		t,
		`window.Alpine && document.querySelector('form [name=description]')`,
	)
	name := "Body policy " + uuid.NewString()
	b.evaluate(t, fmt.Sprintf(`(() => {
 const form = document.querySelector('form[x-data="projectSaveFeedback"]');
 form.querySelector('[name=name]').value = %q;
 form.querySelector('[name=description]').value = 'x'.repeat(3000);
 document.addEventListener('htmx:afterRequest', event => {
  window.bodyPolicyStatus = event.detail.xhr.status;
 }, {once:true});
 form.requestSubmit(); return true;
})()`, name))
	b.wait(
		t,
		`window.bodyPolicyStatus === 413 && !document.querySelector('.htmx-request')`,
	)
	if string(b.evaluate(t, fmt.Sprintf(`(async () => {
 const response = await fetch('/api/v1/projects', {headers:{Authorization:%q}});
 if (response.status !== 200) throw new Error('project API failed');
 return !(await response.json()).items.some(project => project.name === %q);
})()`, "Bearer "+token, name))) != "true" {
		t.Fatal("oversized browser form created a project")
	}
	b.screenshot(t, "configured-body-rejected")
	b.evaluate(t, `(() => {
 const form = document.querySelector('form[x-data="projectSaveFeedback"]');
 form.querySelector('[name=description]').value = 'configured form accepted';
 form.requestSubmit(); return true;
})()`)
	b.wait(
		t,
		fmt.Sprintf(
			`location.pathname !== '/projects/new' && document.body.innerText.includes(%q)`,
			name,
		),
	)
	b.screenshot(t, "configured-body-accepted")
}
