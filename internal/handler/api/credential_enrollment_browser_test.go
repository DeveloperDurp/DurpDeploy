//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/repository"
	"github.com/stretchr/testify/require"
)

func TestCredentialEnrollmentBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	var lifecycle struct{ ID int64 }
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"POST",
			"/api/v1/lifecycles",
			map[string]string{"name": "Browser enrollment lifecycle"},
			201,
		),
		&lifecycle,
	)
	f.api(
		t,
		"PUT",
		f.base(),
		map[string]any{"name": f.project.Name, "lifecycle_id": lifecycle.ID},
		200,
	)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	for _, base := range []string{f.base(), fmt.Sprintf("/api/v1/lifecycles/%d", lifecycle.ID)} {
		web := strings.TrimPrefix(base, "/api/v1")
		var ordinary struct{ ID int64 }
		decodeLifecycleTest(
			t,
			f.api(
				t,
				"POST",
				base+"/variables",
				map[string]any{
					"name":   "ENROLL_ME",
					"value":  "private-enrollment-browser",
					"secret": true,
				},
				201,
			),
			&ordinary,
		)
		b.navigateBackTest(t, f.baseURL+web+"/credentials")
		b.wait(
			t,
			`document.body.textContent.includes('No credentials are configured')`,
		)
		b.navigateBackTest(t, f.baseURL+web+"/credentials/new")
		selector := fmt.Sprintf(
			"[data-enrollment-id=%q]",
			fmt.Sprint(ordinary.ID),
		)
		b.wait(t, fmt.Sprintf("!!document.querySelector(%q)", selector))
		require.Equal(
			t,
			"true",
			string(
				b.evaluate(
					t,
					`!document.documentElement.outerHTML.includes('private-enrollment-browser')`,
				),
			),
		)
		b.evaluate(
			t,
			fmt.Sprintf(
				"document.querySelector(%q).requestSubmit(); true",
				selector,
			),
		)
		b.wait(
			t,
			fmt.Sprintf(
				`location.pathname === %q && !!document.querySelector('[data-credential-id="%d"]') && !!document.querySelector('main .alert-success')`,
				web+"/credentials",
				ordinary.ID,
			),
		)
		b.navigateBackTest(t, f.baseURL+web+"/variables")
		b.wait(t, `!document.body.textContent.includes('ENROLL_ME')`)
		b.navigateBackTest(t, f.baseURL+web+"/credentials/new")
		b.wait(t, `!!document.querySelector('[data-credential-create]')`)
		b.evaluate(
			t,
			`{ const form=document.querySelector('[data-credential-create]'); form.querySelector('[name=name]').value='WEB_CREDENTIAL'; form.querySelector('[name=value]').value='private-new-browser'; form.querySelector('[name=expires_at]').value='2000-01-01T00:00'; form.requestSubmit(); } true`,
		)
		b.wait(
			t,
			`!!document.querySelector('.alert-success') && document.body.textContent.includes('WEB_CREDENTIAL')`,
		)
		var rows []repository.Credential
		decodeLifecycleTest(
			t,
			f.api(t, "GET", base+"/credentials", nil, 200),
			&rows,
		)
		var created repository.Credential
		for _, row := range rows {
			if row.Name == "WEB_CREDENTIAL" {
				created = row
			}
		}
		require.Positive(t, created.ID)
		b.navigateBackTest(t, f.baseURL+strings.TrimPrefix(f.base(), "/api/v1"))
		b.wait(t, `!!document.querySelector('[data-credential-banner]')`)
		require.Equal(
			t,
			"true",
			string(
				b.evaluate(
					t,
					`document.querySelector('[data-credential-banner]').textContent.includes('1 expired')`,
				),
			),
		)
		for _, width := range []int{375, 1280} {
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
			require.Equal(
				t,
				"true",
				string(
					b.evaluate(
						t,
						`document.documentElement.scrollWidth <= innerWidth`,
					),
				),
			)
			b.screenshot(
				t,
				fmt.Sprintf("credential-banner-%s-%d", created.Kind, width),
			)
		}
		b.navigateBackTest(
			t,
			fmt.Sprintf(
				"%s%s/credentials/%s/%d/delete",
				f.baseURL,
				web,
				created.Kind,
				created.ID,
			),
		)
		b.wait(t, `!!document.querySelector('button.btn-error')`)
		b.evaluate(
			t,
			`document.querySelector('button.btn-error').click(); true`,
		)
		b.wait(
			t,
			`!!document.querySelector('.alert-success') && !document.body.textContent.includes('WEB_CREDENTIAL')`,
		)
		// Retain the deletion example for manual review after this test.
		f.api(
			t,
			"POST",
			base+"/credentials",
			map[string]any{
				"name":       "RETAINED_WEB_CREDENTIAL",
				"value":      "retained-browser-example",
				"expires_at": 4070908800,
			},
			201,
		)
	}
}
