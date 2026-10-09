//go:build e2e && packagebrowser

package api_test

import "testing"

func TestThemeStylesBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	b.navigateBackTest(t, f.baseURL+"/environments/new")
	b.captureNavigation(t, "theme-form", func() {
		if string(b.evaluate(t, `(() => {
 const heading = document.querySelector('main h1');
 const field = document.querySelector('main input[name="name"]');
 const label = field.closest('.form-control').querySelector('.label');
 const primary = document.querySelector('main .btn-primary');
 const secondary = document.querySelector('main .btn-ghost');
 const root = getComputedStyle(document.documentElement);
 return getComputedStyle(heading).fontWeight === '700' &&
 Math.abs(field.getBoundingClientRect().width - field.parentElement.clientWidth) < 2 &&
 field.getBoundingClientRect().height >= 44 &&
 getComputedStyle(label).color === root.color &&
 getComputedStyle(primary).backgroundColor !== getComputedStyle(secondary).backgroundColor &&
 (innerWidth >= 768 || primary.getBoundingClientRect().height >= 44);
})()`)) != "true" {
			t.Fatal(
				"theme styles change field widths, colors, or touch targets",
			)
		}
	})
}
