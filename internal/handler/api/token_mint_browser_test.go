//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestTokenMintCeilingBrowserE2E(t *testing.T) {
	base := strings.TrimRight(os.Getenv("DURPDEPLOY_LIVE_BASE"), "/")
	session := os.Getenv("DURPDEPLOY_LIVE_MINT_SESSION")
	if base == "" {
		f := newArtifactE2E(t)
		tokenMintOwner(t, f)
		base, session = f.baseURL, f.session
	}
	if session == "" {
		t.Fatal("live ceiling browser check requires a Maintainer session")
	}
	b := startLivePackageBrowser(t, base)
	b.setBackTestSession(t, base, session)
	b.navigateBackTest(t, base+"/settings/tokens")
	b.waitTokenSettingsReady(t)
	b.evaluate(t, `(() => {
 const form=document.querySelector('form[action="/settings/tokens"]');
 form.elements.name.value='ceiling-browser-rejected';
 form.elements.role.value='admin'; form.requestSubmit(); return true;
})()`)
	b.wait(
		t,
		`document.querySelector('.alert-error')?.textContent.includes('Token role exceeds owner permissions')`,
	)
	if string(
		b.evaluate(
			t,
			`!document.querySelector('.alert code') && ![...document.querySelectorAll('[data-token-name]')].some(el=>el.textContent.trim()==='ceiling-browser-rejected')`,
		),
	) != "true" {
		t.Fatal("rejected browser mint displayed or stored a token")
	}
	for _, width := range []int{375, 1280} {
		b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width": width, "height": 900, "deviceScaleFactor": 1,
			"mobile": false,
		}, &struct{}{})
		if string(
			b.evaluate(t, `document.documentElement.scrollWidth<=innerWidth`),
		) != "true" {
			t.Fatal("ceiling error overflows viewport")
		}
		b.screenshot(t, fmt.Sprintf("token-mint-rejected-%d", width))
	}
	b.waitTokenSettingsReady(t)
	b.evaluate(t, `(() => {
 const form=document.querySelector('form[action="/settings/tokens"]');
 form.elements.name.value='ceiling-browser-allowed';
 form.elements.role.value='maintainer'; form.requestSubmit(); return true;
})()`)
	b.wait(
		t,
		`document.querySelector('.alert code') && location.search.includes('flash=')`,
	)
	// Follow the normal dismissal before capturing evidence without credentials.
	b.evaluate(
		t,
		`document.querySelector('.alert a[href="/settings/tokens"]').click(); true`,
	)
	b.wait(
		t,
		`!document.querySelector('.alert code') && location.search==='' && [...document.querySelectorAll('[data-token-name]')].some(el=>el.textContent.trim()==='ceiling-browser-allowed')`,
	)
	b.screenshot(t, "token-mint-allowed")
}
