//go:build e2e && packagebrowser

package api_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestScreenshotRenderingReadinessBrowserE2E(t *testing.T) {
	browser := startPackageBrowser(t)
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/slow.svg" {
				time.Sleep(150 * time.Millisecond)
				w.Header().Set("Content-Type", "image/svg+xml")
				_, _ = w.Write(
					[]byte(
						`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"/>`,
					),
				)
				return
			}
			_, _ = w.Write(
				[]byte(
					`<!doctype html><div style="height:3000px"></div><img id="lazy" loading="lazy" width="10" height="10" src="/slow.svg">`,
				),
			)
		}),
	)
	t.Cleanup(server.Close)
	browser.evaluate(t, "location.href="+strconv.Quote(server.URL))
	browser.wait(t, "document.getElementById('lazy') !== null")
	browser.evaluate(t, `(() => {
		document.body.animate([{opacity: 0.5}, {opacity: 1}], {duration: 250});
		return true;
	})()`)
	browser.screenshot(t, "screenshot-rendering-readiness")
	if string(browser.evaluate(t, `scrollY === 0 &&
		document.getElementById('lazy').naturalWidth > 0 &&
		document.getAnimations().every(animation => animation.playState !== 'running')`)) != "true" {
		t.Fatal(
			"screenshot captured unfinished rendering or left the page scrolled",
		)
	}
}
