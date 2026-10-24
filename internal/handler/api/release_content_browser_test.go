//go:build e2e && packagebrowser

package api_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestReleaseContentBrowserE2E(t *testing.T) {
	fixture := newReleaseContent(t)
	f := fixture.f
	_, viewerSession := fixture.viewer(t)
	b := startPackageBrowser(t)
	b.call(t, "Emulation.setTouchEmulationEnabled",
		map[string]any{"enabled": true}, &struct{}{})
	for _, identity := range []struct{ name, session string }{
		{"admin", f.session}, {"viewer", viewerSession},
	} {
		b.setBackTestSession(t, f.baseURL, identity.session)
		for _, width := range []int{375, 390, 768, 1280} {
			b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
				"width": width, "height": 844, "deviceScaleFactor": 1, "mobile": false,
			}, &struct{}{})
			b.navigateBackTest(t,
				f.baseURL+strings.TrimPrefix(fixture.path, "/api/v1"))
			if string(b.evaluate(
				t,
				`document.querySelectorAll('details[data-release-script]').length === 2 && !document.querySelector('details[data-release-script][open], details[data-release-variable-name]') && !document.body.textContent.includes('View full variable name')`,
			)) != "true" {
				t.Fatal("full release content has no closed disclosures")
			}
			for index := range fixture.scripts {
				selector := fmt.Sprintf(
					"[data-release-script-index='%d']", index,
				)
				checkReleaseDisclosure(t, b, selector, fixture.scripts[index])
			}
			for _, name := range fixture.names {
				checkReleaseVariableName(t, b, len(fixture.names), name)
			}
			if string(
				b.evaluate(
					t,
					`!window.releaseContentInjected && !document.body.textContent.includes('release-content-secret-sentinel') && document.documentElement.scrollWidth <= innerWidth`,
				),
			) != "true" {
				t.Fatal("release content escaped its boundary")
			}
			b.screenshot(t, fmt.Sprintf("release-content-%s-%d",
				identity.name, width))
		}
	}
	fixture.checkAPI(t)
}

func checkReleaseVariableName(
	t *testing.T, b *packageBrowser, count int, name string,
) {
	t.Helper()
	if string(b.evaluate(t, fmt.Sprintf(`(() => {
 const cells = document.querySelectorAll('[data-release-variable-name]');
 const name = %q;
 const cell = [...cells].find(element => element.textContent === name);
 return cells.length === %d && !!cell && cell.tagName === 'TD' &&
  cell.getClientRects().length > 0 &&
  cell.scrollWidth <= cell.clientWidth && cell.scrollHeight <= cell.clientHeight &&
  getComputedStyle(cell).whiteSpace !== 'nowrap' &&
  !cell.querySelector('details, summary, button, a') &&
  [...document.querySelectorAll('td')].filter(e => e.textContent === name).length === 1;
})()`, name, count))) != "true" {
		t.Fatalf("saved variable %q is repeated, clipped, or hidden", name)
	}
}

func checkReleaseDisclosure(
	t *testing.T, b *packageBrowser, selector, content string,
) {
	t.Helper()
	// Open with an actual touch event, then close and reopen with Enter.
	var point struct{ X, Y float64 }
	raw := b.evaluate(t, fmt.Sprintf(`(() => {
 const summary = document.querySelector(%q + ' > summary');
 summary.scrollIntoView({block:'center'});
 const box = summary.getBoundingClientRect();
 if (box.height < 44 || box.left < 0 || box.right > innerWidth) throw new Error('disclosure control does not fit');
 return {X:box.left + box.width / 2, Y:box.top + box.height / 2};
})()`, selector))
	if err := json.Unmarshal(raw, &point); err != nil {
		t.Fatal(err)
	}
	b.call(t, "Input.dispatchTouchEvent", map[string]any{
		"type": "touchStart", "touchPoints": []map[string]float64{
			{"x": point.X, "y": point.Y},
		},
	}, &struct{}{})
	b.call(t, "Input.dispatchTouchEvent", map[string]any{
		"type": "touchEnd", "touchPoints": []any{},
	}, &struct{}{})
	b.wait(t, fmt.Sprintf(`document.querySelector(%q).open`, selector))
	if string(b.evaluate(t, fmt.Sprintf(`(() => {
 const details = document.querySelector(%q);
 const full = details.querySelector('[data-release-full-content]');
 return full.textContent === %q && full.getClientRects().length > 0 &&
 full.scrollWidth <= full.clientWidth && document.documentElement.scrollWidth <= innerWidth;
})()`, selector, content))) != "true" {
		t.Fatal("full release content is truncated or overflows")
	}
	for _, open := range []bool{false, true} {
		b.evaluate(t, fmt.Sprintf(
			`document.querySelector(%q + ' > summary').focus(); true`,
			selector,
		))
		for _, kind := range []string{"keyDown", "keyUp"} {
			event := map[string]any{
				"type": kind, "key": "Enter", "code": "Enter",
				"windowsVirtualKeyCode": 13,
			}
			if kind == "keyDown" {
				event["text"] = "\r"
			}
			b.call(t, "Input.dispatchKeyEvent", event, &struct{}{})
		}
		b.wait(t, fmt.Sprintf(`document.querySelector(%q).open === %t`,
			selector, open))
	}
}
