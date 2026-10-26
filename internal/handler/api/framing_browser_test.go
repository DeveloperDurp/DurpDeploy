//go:build e2e && packagebrowser

package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestFramingPolicyBrowserE2E(t *testing.T) {
	b := startPackageBrowser(t, true)
	f := newFramingE2E(t)
	b.call(t, "Audits.enable", struct{}{}, &struct{}{})
	var cookie struct{ Success bool }
	b.call(t, "Network.setCookie", map[string]any{
		"name": "session", "value": f.session, "url": f.baseURL,
		"httpOnly": true, "sameSite": "Lax",
	}, &cookie)
	if !cookie.Success {
		t.Fatal("admin session cookie was not set")
	}
	b.navigateBackTest(t, f.baseURL+"/projects")
	b.evaluate(
		t,
		`document.querySelector('a[href="/environments"]').click(); true`,
	)
	b.waitBackTestURL(t, f.baseURL+"/environments")
	if string(
		b.evaluate(t, `performance.getEntriesByType('navigation')[0].name`),
	) != fmt.Sprintf(
		"%q",
		f.baseURL+"/projects",
	) {
		t.Fatal("HTMX navigation became a full document reload")
	}
	parent := httptest.NewServer(http.HandlerFunc(framingParent))
	t.Cleanup(parent.Close)
	for _, scenario := range []struct {
		name, parent, path, site string
		allowed, authenticated   bool
	}{
		{"same-origin", f.baseURL + "/framing-parent", "/projects", "same-origin", true, true},
		{"same-site", parent.URL + "/", "/projects", "same-site", false, true},
		{"cross-site", strings.Replace(parent.URL, "127.0.0.1", "localhost", 1) + "/", "/projects", "cross-site", false, false},
		{"login", parent.URL + "/", "/login", "same-site", false, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Discard only observations from the preceding document.
			for len(f.frames) > 0 {
				<-f.frames
			}
			b.wire.events = nil
			address := scenario.parent + "?src=" + url.QueryEscape(
				f.baseURL+scenario.path,
			)
			b.call(t, "Page.navigate", map[string]string{
				"url": address,
			}, &struct{}{})
			b.wait(
				t,
				fmt.Sprintf(
					`location.href === %q && document.body.dataset.loaded === 'true'`,
					address,
				),
			)
			select {
			case seen := <-f.frames:
				if seen.path != scenario.path || seen.site != scenario.site ||
					seen.authenticated != scenario.authenticated {
					t.Fatalf("iframe request=%+v", seen)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("iframe request did not reach the application")
			}
			if scenario.allowed {
				b.wait(
					t,
					`document.querySelector('#target').contentDocument?.querySelector('h1')?.textContent.includes('Projects')`,
				)
			} else {
				assertFrameAncestorBlocked(t, b)
			}
			b.screenshot(t, "framing-"+scenario.name)
		})
	}
	b.setBackTestSession(t, f.baseURL, f.viewer)
	b.navigateBackTest(t, f.baseURL+"/projects")
	b.wait(
		t,
		`document.body.textContent.includes('(viewer)') && ![...document.querySelectorAll('button')].some(b => b.textContent.includes('New Project'))`,
	)
	b.navigateBackTest(t, f.baseURL+"/login")
	b.wait(t, `document.querySelector('form') && window.Alpine`)
}

func assertFrameAncestorBlocked(t *testing.T, b *packageBrowser) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		// Drain protocol events while checking the committed child frame.
		b.call(t, "Page.getFrameTree", struct{}{}, &struct{}{})
		for _, event := range b.wire.events {
			if event.Method != "Audits.issueAdded" ||
				event.Session != b.session {
				continue
			}
			var params struct {
				Issue struct {
					Code    string
					Details struct {
						CSP struct {
							Directive  string `json:"violatedDirective"`
							ReportOnly bool   `json:"isReportOnly"`
						} `json:"contentSecurityPolicyIssueDetails"`
					}
				}
			}
			if err := json.Unmarshal(event.Parameters.Issue, &params.Issue); err != nil {
				t.Fatal(err)
			}
			if params.Issue.Code == "ContentSecurityPolicyIssue" &&
				strings.HasPrefix(
					params.Issue.Details.CSP.Directive,
					"frame-ancestors",
				) &&
				!params.Issue.Details.CSP.ReportOnly {
				t.Log("Browser enforced frame-ancestors on the child response")
				return
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("browser did not report enforced frame-ancestors rejection")
}
