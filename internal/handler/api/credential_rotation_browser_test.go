//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"github.com/stretchr/testify/require"
)

func TestCredentialRotationBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	id := rotationAPIVariable(
		t,
		f,
		f.base(),
		"PROJECT_TOKEN",
		"browser-original",
	)
	first := verificationRelease(t, f, "before-rotation-first")
	second := verificationRelease(t, f, "before-rotation-second")
	var lc struct{ ID int64 }
	decodeLifecycleTest(t, f.api(t, "POST", "/api/v1/lifecycles",
		map[string]string{"name": "Browser credential lifecycle"}, 201), &lc)
	lifecycleBase := fmt.Sprintf("/api/v1/lifecycles/%d", lc.ID)
	shared := rotationAPIVariable(
		t,
		f,
		lifecycleBase,
		"SHARED_TOKEN",
		"browser-original",
	)
	f.api(
		t,
		"PUT",
		f.base(),
		map[string]any{"name": f.project.Name, "lifecycle_id": lc.ID},
		200,
	)
	f.api(t, "PUT", f.base()+"/package-repository", map[string]string{
		"url_template": f.upstream + "/{version}.zip",
		"auth_type":    "bearer",
		"credential":   "browser-original",
	}, 200)
	var source struct{ ID int64 }
	decodeLifecycleTest(
		t,
		f.api(t, "GET", f.base()+"/package-repository", nil, 200),
		&source,
	)
	verificationRelease(t, f, "before-retained-rotation")
	f.api(t, "DELETE", f.base()+"/package-repository", nil, 204)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	for _, test := range []struct {
		base, kind string
		id         int64
	}{
		{f.base(), "variable", id}, {lifecycleBase, "lifecycle", shared}, {f.base(), "package", source.ID},
	} {
		selector := fmt.Sprintf(
			"[data-credential-kind=%q][data-credential-id=%q]",
			test.kind,
			fmt.Sprint(test.id),
		)
		web := strings.TrimPrefix(test.base, "/api/v1") + "/credentials"
		edit := fmt.Sprintf("%s/%s/%d/edit", web, test.kind, test.id)
		b.navigateBackTest(t, f.baseURL+edit)
		b.wait(t, fmt.Sprintf("!!document.querySelector(%q)", selector))
		require.Equal(t, "1", string(b.evaluate(
			t,
			`document.querySelectorAll('[data-credential-form] button[type=submit]').length`,
		)))
		require.Equal(t, `"Save"`, string(b.evaluate(
			t,
			`document.querySelector('[data-credential-form] button[type=submit]').textContent`,
		)))
		require.Equal(t, "true", string(b.evaluate(
			t,
			`[...document.querySelectorAll('[name=value]')].every(e=>e.type==='password' && e.value==='') && !document.documentElement.outerHTML.includes('browser-original')`,
		)))
		b.evaluate(
			t,
			fmt.Sprintf(
				`{ const card=document.querySelector(%q); card.querySelector('[name=expires_at]').value='2000-01-01T00:00'; card.querySelector('button[type=submit]').click(); } true`,
				selector,
			),
		)
		b.wait(
			t,
			fmt.Sprintf(
				`document.querySelector(%q)?.querySelector('[name=revision]')?.value==='1' && !!document.querySelector('.alert-success')`,
				selector,
			),
		)
		require.Equal(
			t,
			`"expired"`,
			string(
				b.evaluate(
					t,
					fmt.Sprintf(
						`document.querySelector(%q).querySelector('[data-credential-status]').textContent`,
						selector,
					),
				),
			),
		)
		for _, width := range []int{375, 1280} {
			b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
				"width":             width,
				"height":            900,
				"deviceScaleFactor": 1,
				"mobile":            false,
			}, &struct{}{})
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
			b.screenshot(t, fmt.Sprintf("credential-%s-%d", test.kind, width))
		}
		b.evaluate(
			t,
			fmt.Sprintf(
				`{ const card=document.querySelector(%q); card.querySelector('[name=value]').value='   '; card.querySelector('button[type=submit]').click(); } true`,
				selector,
			),
		)
		b.wait(
			t,
			`!!document.querySelector('.alert-error') && document.body.textContent.includes('nonempty replacement')`,
		)
		b.evaluate(
			t,
			fmt.Sprintf(
				`{ const card=document.querySelector(%q); card.querySelector('[name=expires_at]').value='2099-01-01T00:00'; card.querySelector('[name=value]').value='browser-replacement'; card.querySelector('button[type=submit]').click(); } true`,
				selector,
			),
		)
		b.wait(
			t,
			fmt.Sprintf(
				`document.querySelector(%q)?.querySelector('[name=revision]')?.value==='2' && !!document.querySelector('.alert-success')`,
				selector,
			),
		)
		require.Equal(
			t,
			"true",
			string(
				b.evaluate(
					t,
					`!document.documentElement.outerHTML.includes('browser-replacement') && [...document.querySelectorAll('[name=value]')].every(e=>e.value==='')`,
				),
			),
		)
		b.screenshot(t, "credential-"+test.kind+"-rotated")
		rows := f.api(t, "GET", test.base+"/credentials", nil, 200)
		require.NotContains(t, string(rows), "browser-replacement")
		rotated := rotationAPICredential(t, rows, test.kind, test.id)
		require.Positive(t, rotated.RotatedAt)
		b.evaluate(
			t,
			`document.querySelector('[data-credential-form] button[type=submit]').click(); true`,
		)
		b.wait(t, fmt.Sprintf(
			`document.querySelector(%q)?.querySelector('[name=revision]')?.value==='3' && !!document.querySelector('.alert-success')`,
			selector,
		))
		current := rotationAPICredential(t,
			f.api(t, "GET", test.base+"/credentials", nil, 200),
			test.kind, test.id)
		require.Equal(t, rotated.RotatedAt, current.RotatedAt)
	}
	for _, release := range []db.Release{first, second} {
		values, err := f.h.repo.ListReleaseVariablesByRelease(
			t.Context(),
			release.ID,
		)
		require.NoError(t, err)
		require.Equal(t, "browser-replacement", values[0].Value.String)
	}
	require.Equal(
		t,
		"null",
		strings.TrimSpace(
			string(f.api(t, "GET", f.base()+"/package-repository", nil, 200)),
		),
	)
	viewer := seedAPIUser(
		t,
		f.h.repo,
		"credential-viewer@example.test",
		"viewer",
	)
	tstAddMember(t, f.h, f.project.ID, viewer.ID, "viewer")
	session, csrf, err := auth.NewSessionToken()
	require.NoError(t, err)
	_, err = f.h.repo.Queries.CreateSession(t.Context(), db.CreateSessionParams{
		ID:        session,
		UserID:    viewer.ID,
		CsrfToken: csrf,
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	})
	require.NoError(t, err)
	b.setBackTestSession(t, f.baseURL, session)
	b.navigateBackTest(
		t,
		fmt.Sprintf("%s/projects/%d/credentials", f.baseURL, f.project.ID),
	)
	b.wait(t, `document.body.textContent.includes('PROJECT_TOKEN')`)
	require.Equal(
		t,
		"0",
		string(
			b.evaluate(
				t,
				`document.querySelectorAll('[data-credential-form]').length`,
			),
		),
	)
	b.screenshot(t, "credentials-viewer")
	for _, suffix := range []string{"new", fmt.Sprintf("variable/%d/edit", id), fmt.Sprintf("variable/%d/delete", id)} {
		b.navigateBackTest(
			t,
			fmt.Sprintf(
				"%s/projects/%d/credentials/%s",
				f.baseURL,
				f.project.ID,
				suffix,
			),
		)
		b.wait(
			t,
			`document.body.textContent.includes('Viewers cannot manage credentials')`,
		)
		require.Equal(
			t,
			"0",
			string(
				b.evaluate(
					t,
					`document.querySelectorAll('[data-credential-form], [data-credential-create], [data-credential-enrollment], button.btn-error').length`,
				),
			),
		)
	}
	f.session, f.csrf = session, csrf
	f.web(
		t,
		"POST",
		fmt.Sprintf("/projects/%d/credentials/variable/%d", f.project.ID, id),
		nil,
		403,
	)
}

func rotationAPICredential(t *testing.T, rows []byte,
	kind string, id int64,
) repository.Credential {
	t.Helper()
	var credentials []repository.Credential
	decodeLifecycleTest(t, rows, &credentials)
	for _, credential := range credentials {
		if credential.Kind == kind && credential.ID == id {
			return credential
		}
	}
	t.Fatalf("credential %s/%d not found", kind, id)
	return repository.Credential{}
}
