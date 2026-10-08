//go:build e2e && packagebrowser

package api_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func TestVerificationCleanupRetryBrowserE2E(t *testing.T) {
	for _, kind := range []string{"deployment", "runbook"} {
		t.Run(kind, func(t *testing.T) {
			f := newVerificationE2E(t)
			release := verificationRelease(t, f, "cleanup-retry")
			var deploymentID int64
			var webPath, apiPath, retryPath, selector string
			if kind == "deployment" {
				result, err := f.h.repo.CreateDeployment(
					t.Context(),
					db.CreateDeploymentParams{
						ReleaseID:     release.ID,
						EnvironmentID: f.environment.ID,
						Status:        "pending",
					},
				)
				if err != nil {
					t.Fatal(err)
				}
				deploymentID = result.Deployment.ID
				webPath = fmt.Sprintf("/deployments/%d", deploymentID)
				apiPath = "/api/v1" + webPath
				retryPath = apiPath + "/retry"
				selector = "button[hx-post$='/redeploy']"
			} else {
				book, version, err := f.h.repo.SaveRunbook(
					t.Context(),
					repository.RunbookSave{
						ProjectID: f.project.ID,
						Name:      "cleanup-retry",
						StepsJSON: `[{"name":"Retry","script_body":"echo retry","container_image":"docker.io/library/bash:5.2"}]`,
					},
				)
				if err != nil {
					t.Fatal(err)
				}
				execution, _, err := f.h.repo.CreateRunbookExecution(
					t.Context(),
					repository.RunbookExecutionRequest{
						ProjectID:     f.project.ID,
						RunbookID:     book.ID,
						VersionID:     version.ID,
						EnvironmentID: f.environment.ID,
					},
				)
				if err != nil {
					t.Fatal(err)
				}
				deploymentID = execution.DeploymentID
				webPath = fmt.Sprintf(
					"/projects/%d/runbooks/executions/%d",
					f.project.ID,
					execution.ID,
				)
				apiPath = fmt.Sprintf(
					"%s/runbook-executions/%d",
					f.base(),
					execution.ID,
				)
				retryPath = apiPath + "/retry"
				selector = "form[action$='/retry'] button[type=submit]"
			}
			if _, err := f.h.repo.DB.Exec(
				"UPDATE deployments SET status='cleanup_unconfirmed' WHERE id=?",
				deploymentID,
			); err != nil {
				t.Fatal(err)
			}
			if _, err := f.h.repo.DB.Exec(`INSERT INTO agents(id,name,endpoint)
VALUES('cleanup','Cleanup','https://agent.example');
INSERT INTO remote_step_runs(deployment_id,step_index,agent_id,state)
VALUES(?,0,'cleanup','cleanup_unconfirmed')`, deploymentID); err != nil {
				t.Fatal(err)
			}
			actions := []string{retryPath}
			if kind == "deployment" {
				actions = append(actions, apiPath+"/redeploy")
			}
			for _, path := range actions {
				f.api(t, "POST", path, nil, 409)
			}
			if _, err := f.h.repo.DB.Exec(
				"UPDATE remote_step_runs SET cleanup_confirmed_at=1",
			); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(
				string(f.api(t, "GET", apiPath, nil, 200)),
				"cleanup_unconfirmed",
			) {
				t.Fatal("historical cleanup outcome missing")
			}
			for _, path := range actions {
				var retried struct {
					ID int64 `json:"id"`
				}
				if err := json.Unmarshal(
					f.api(t, "POST", path, nil, 201),
					&retried,
				); err != nil {
					t.Fatal(err)
				}
				if kind == "runbook" {
					waitExecutionStatus(t, f, fmt.Sprintf(
						"%s/runbook-executions/%d", f.base(), retried.ID,
					), "succeeded")
				} else {
					waitVerificationStatus(t, f, retried.ID, "succeeded")
				}
			}
			browser := startPackageBrowser(t)
			browser.call(
				t,
				"Network.setCookie",
				map[string]any{
					"name":     "session",
					"value":    f.session,
					"url":      f.baseURL,
					"httpOnly": true,
				},
				&struct{}{},
			)
			browser.call(
				t,
				"Page.navigate",
				map[string]string{"url": f.baseURL + webPath},
				&struct{}{},
			)
			browser.wait(
				t,
				"document.readyState === 'complete' && window.htmx && document.querySelector("+fmt.Sprintf(
					"%q",
					selector,
				)+") !== null",
			)
			browser.wait(
				t,
				`document.querySelector('#status-badge').getAttribute('hx-trigger') === 'none'`,
			)
			for _, width := range []int{375, 768, 1280} {
				browser.call(
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
				browser.wait(
					t,
					"document.documentElement.scrollWidth <= innerWidth",
				)
				browser.screenshot(
					t,
					fmt.Sprintf("cleanup-retry-%s-%d", kind, width),
				)
			}
			browser.evaluate(
				t,
				"window.confirm = () => true; document.querySelector("+fmt.Sprintf(
					"%q",
					selector,
				)+").click(); true",
			)
			waitCleanupRetryNavigation(
				t,
				browser,
			)
			var count int
			if err := f.h.repo.DB.QueryRow("SELECT count(*) FROM deployments").
				Scan(&count); err != nil ||
				count != len(actions)+2 {
				t.Fatalf("browser retry count=%d error=%v", count, err)
			}
		})
	}
}

func waitCleanupRetryNavigation(t *testing.T, browser *packageBrowser) {
	t.Helper()
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		var result struct {
			Result struct {
				Value bool `json:"value"`
			} `json:"result"`
		}
		err := browser.wire.call("Runtime.evaluate", browser.session,
			map[string]any{
				"expression":    `document.readyState === 'complete' && document.querySelector('#status-badge')?.innerText.includes('succeeded') === true`,
				"returnByValue": true,
			}, &result)
		if err == nil && result.Result.Value {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("retry navigation did not finish: %v", err)
		case <-ticker.C:
		}
	}
}
