//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestProjectTokensBrowserE2E(t *testing.T) {
	base := strings.TrimRight(os.Getenv("DURPDEPLOY_LIVE_BASE"), "/")
	session := os.Getenv("DURPDEPLOY_LIVE_SESSION")
	var projects []string
	if base == "" {
		f := newArtifactE2E(t)
		other := seedProject(t, f.h.repo)
		base, session = f.baseURL, f.session
		projects = []string{strconv.FormatInt(f.project.ID, 10),
			strconv.FormatInt(other.ID, 10)}
	} else {
		projects = strings.Split(
			os.Getenv("DURPDEPLOY_LIVE_TOKEN_PROJECTS"),
			",",
		)
	}
	if len(projects) != 2 || projects[0] == "" || session == "" {
		t.Fatal(
			"token browser suite requires two retained projects and a session",
		)
	}
	b := startPackageBrowser(t)
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		b.evaluate(
			t,
			`document.querySelectorAll('.alert code').forEach(code=>code.textContent='[redacted]'); true`,
		)
		t.Logf(
			"token UI state: %s",
			b.evaluate(
				t,
				`({path:location.pathname, errors:[...document.querySelectorAll('.alert-error')].map(e=>e.textContent), rows:[...document.querySelectorAll('tbody tr')].map(row=>({name:row.cells[0].textContent,access:row.cells[1].textContent,scope:row.querySelector('[name=scope]')?.value,role:row.querySelector('[name=role]')?.value,projects:[...row.querySelectorAll('select[name=project_ids] option:checked')].map(o=>o.value)}))})`,
			),
		)
		b.screenshot(t, "project-token-failure")
	})
	b.call(
		t,
		"Security.setIgnoreCertificateErrors",
		map[string]bool{"ignore": true},
		&struct{}{},
	)
	b.setBackTestSession(t, base, session)
	b.navigateBackTest(t, base+"/settings/tokens")
	b.wait(
		t,
		`document.querySelector('#new-token-role') && !document.querySelector('.htmx-request')`,
	)
	if string(
		b.evaluate(
			t,
			`document.querySelector('#new-token-scope').value === 'scoped' && document.querySelector('#new-token-role').value === 'viewer' && document.querySelectorAll('#new-token-projects option:checked').length === 0`,
		),
	) != "true" {
		t.Fatal(
			"new token form did not default to a viewer with no selected projects",
		)
	}
	name := "scope-browser-" + uuid.NewString()
	// Create a deployer token with two selected projects through the form.
	b.evaluate(t, fmt.Sprintf(`(() => {
 const form = document.querySelector('form[action="/settings/tokens"]');
 form.elements.name.value = %q;
 form.elements.scope.value = 'scoped';
 form.elements.role.value = 'deployer';
 for (const option of form.elements.project_ids.options) option.selected = [%q,%q].includes(option.value);
 form.requestSubmit(); return true;
})()`, name, projects[0], projects[1]))
	b.wait(
		t,
		`location.pathname === '/settings/tokens' && location.search.includes('flash=') && document.querySelector('.alert code')`,
	)
	if string(
		b.evaluate(
			t,
			`/^ddp_pat_[0-9a-f]{64}$/.test(document.querySelector('.alert code').textContent.trim())`,
		),
	) != "true" {
		t.Fatal("new token was not shown once")
	}
	// Do not capture the bearer credential in screenshots.
	b.evaluate(
		t,
		`document.querySelector('.alert a[href="/settings/tokens"]').click(); true`,
	)
	b.wait(
		t,
		`location.search === '' && !document.querySelector('.alert code')`,
	)
	row := fmt.Sprintf(
		`[...document.querySelectorAll('tbody tr')].find(row=>row.cells[0].textContent.trim()===%q)`,
		name,
	)
	if string(
		b.evaluate(
			t,
			fmt.Sprintf(
				`(() => {const row=%s; return !!row && row.cells[1].textContent.includes('deployer') && row.querySelector('[name=scope]').value === 'scoped' && row.querySelectorAll('select[name=project_ids] option:checked').length === 2;})()`,
				row,
			),
		),
	) != "true" {
		t.Fatal("token role or two-project scope missing from management view")
	}
	for _, width := range []int{375, 1280} {
		b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width":             width,
			"height":            900,
			"deviceScaleFactor": 1,
			"mobile":            false,
		}, &struct{}{})
		if string(
			b.evaluate(t, `document.documentElement.scrollWidth <= innerWidth`),
		) != "true" {
			t.Fatalf("token page overflows at width %d", width)
		}
		b.screenshot(t, fmt.Sprintf("project-token-scope-%d", width))
	}
	// Empty scoped selections return a validation message and retain input.
	b.evaluate(t, `(() => {
 const form = document.querySelector('form[action="/settings/tokens"]');
 form.elements.name.value = 'empty-selection'; form.elements.scope.value = 'scoped';
 for (const option of form.elements.project_ids.options) option.selected = false;
 form.requestSubmit(); return true;
})()`)
	b.wait(
		t,
		`document.querySelector('.alert-error')?.textContent.includes('Select a valid role and accessible projects')`,
	)
	b.captureNavigation(t, "project-token-empty-selection")
	// Replace the scope through the owner session without redisplaying the secret.
	b.evaluate(
		t,
		fmt.Sprintf(`(() => {const row=%s; row.querySelector('summary').click();
 const form=row.querySelector('form[data-token-scope-edit]'); form.elements.role.value='viewer';
 for (const option of form.elements.project_ids.options) option.selected = option.value === %q;
 form.requestSubmit(); return true;})()`, row, projects[1]),
	)
	b.wait(
		t,
		fmt.Sprintf(
			`(() => {const row=%s; return location.pathname==='/settings/tokens' && !document.querySelector('.alert-error') && row?.textContent.includes('viewer') && row.querySelectorAll('select[name=project_ids] option:checked').length===1;})()`,
			row,
		),
	)
	if string(
		b.evaluate(t, `!document.querySelector('.alert code')`),
	) != "true" {
		t.Fatal("editing scope redisplayed a bearer credential")
	}
	b.captureNavigation(t, "project-token-edited-scope")
}
