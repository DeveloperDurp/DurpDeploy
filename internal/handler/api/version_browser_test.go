//go:build e2e && agentbrowser

package api_test

import (
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"durpdeploy/internal/buildinfo"
	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/server"
	"github.com/robfig/cron/v3"
)

func TestBuildInfoBrowserE2E(t *testing.T) {
	h := newAPIHarness(t)
	srv := httptest.NewServer(server.NewRouter(h.repo, h.runner,
		cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
		handler.NewAuthHandler(h.repo)))
	t.Cleanup(srv.Close)
	browser := startPackageBrowser(t)
	for _, role := range []string{"admin", "viewer"} {
		user := seedAPIUser(t, h.repo, role+"@version-browser.test", role)
		if _, err := h.repo.Queries.CreateSession(t.Context(),
			db.CreateSessionParams{ID: fmt.Sprint(user.ID), UserID: user.ID,
				CsrfToken: "version-csrf",
				ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
			t.Fatal(err)
		}
		fleetBrowserSession(t, browser, srv.URL, user.ID)
		browser.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width": 1280, "height": 800, "deviceScaleFactor": 1,
			"mobile": false,
		}, &struct{}{})
		fleetBrowserNavigate(t, browser, srv.URL+"/")
		browser.evaluate(t, `(() => {
			const menu = [...document.querySelectorAll('[data-account-menu]')]
				.find(item => item.getClientRects().length > 0);
			menu.open = true;
			menu.querySelector('a[href="/settings/version"]').click();
			return true;
		})()`)
		build := buildinfo.Current()
		browser.wait(t, fmt.Sprintf(
			`document.querySelector('[data-build-version]')?.textContent === %q && document.querySelector('[data-build-number]')?.textContent === %q && document.querySelector('[data-build-commit]')?.textContent === %q`,
			build.Version,
			build.BuildNumber,
			build.Commit,
		))
		browser.screenshot(t, "build-info-"+role+"-desktop")
		browser.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width": 375, "height": 812, "deviceScaleFactor": 1,
			"mobile": false,
		}, &struct{}{})
		browser.wait(t,
			`document.documentElement.scrollWidth <= innerWidth`)
		browser.screenshot(t, "build-info-"+role+"-mobile")
	}
}
