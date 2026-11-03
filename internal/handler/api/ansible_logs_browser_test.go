//go:build e2e && packagebrowser

package api_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/stepdefinition"
)

func TestAnsibleLogFormattingBrowserE2E(t *testing.T) {
	for _, runbook := range []bool{false, true} {
		t.Run(fmt.Sprintf("runbook=%t", runbook), func(t *testing.T) {
			f, definition, _, _ := newAnsibleE2E(t)
			script := stepdefinition.Definition{
				Name:           "Script",
				ScriptBody:     "echo script",
				ContainerImage: definition.ContainerImage,
			}
			f.api(t, "POST", f.base()+"/steps", definition, 201)
			f.api(t, "POST", f.base()+"/steps", script, 201)
			release := verificationRelease(t, f, "formatted-logs")
			var deployment db.Deployment
			var path, apiPath string
			if runbook {
				var book struct{ Runbook db.Runbook }
				decodeLifecycleTest(
					t,
					f.api(t, "POST", f.base()+"/runbooks", map[string]any{
						"name": "Formatted logs", "steps": []stepdefinition.Definition{definition, script},
					}, 201),
					&book,
				)
				execution, result, err := f.h.repo.CreateRunbookExecution(
					t.Context(),
					repository.RunbookExecutionRequest{
						ProjectID: f.project.ID, RunbookID: book.Runbook.ID, EnvironmentID: f.environment.ID,
					},
				)
				if err != nil {
					t.Fatal(err)
				}
				deployment = result.Deployment
				path = fmt.Sprintf(
					"/projects/%d/runbooks/executions/%d",
					f.project.ID,
					execution.ID,
				)
				apiPath = fmt.Sprintf(
					"%s/runbook-executions/%d/logs",
					f.base(),
					execution.ID,
				)
			} else {
				result, err := f.h.repo.CreateDeployment(t.Context(), db.CreateDeploymentParams{
					ReleaseID: release.ID, EnvironmentID: f.environment.ID, Status: "pending",
				})
				if err != nil {
					t.Fatal(err)
				}
				deployment = result.Deployment
				path = fmt.Sprintf("/deployments/%d", deployment.ID)
				apiPath = fmt.Sprintf("/api/v1/deployments/%d/logs", deployment.ID)
			}
			// A refreshed backing release must not change the captured log kind.
			backing, err := f.h.repo.Queries.GetRelease(
				t.Context(),
				deployment.ReleaseID,
			)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.h.repo.Queries.UpdateRelease(t.Context(), db.UpdateReleaseParams{
				ID: backing.ID, ProjectID: f.project.ID, Version: backing.Version, StepsJson: "[]",
			}); err != nil {
				t.Fatal(err)
			}
			heading := "PLAY [all] ****************************************************************"
			seedAnsibleLogLine(t, f, deployment.ID, 0, definition.Name, heading)
			b := startPackageBrowser(t)
			b.setStepLogSession(t, f.baseURL, f.session)
			b.navigateBackTest(t, f.baseURL+path)
			b.wait(
				t,
				`document.querySelector('.ansible-log-line .text-info')?.textContent === 'PLAY [all]'`,
			)
			lines := []string{
				"TASK [configure target] ********************************",
				"ok: [target]", "changed: [target]", "skipping: [target]",
				"[WARNING]: retained warning", "fatal: [target]: UNREACHABLE!",
				"  \"msg\": \"<img src=x onerror=alert(1)> payload ***** ok=99\"",
				"PLAY RECAP *********************************************",
				"target : ok=1 changed=1 unreachable=1 failed=0 skipped=1 rescued=0 ignored=0",
				"other : ok=0 changed=0 unreachable=0 failed=1 skipped=0 rescued=1 ignored=1",
				"long-message-" + strings.Repeat("wrapped-", 50), "",
			}
			for _, line := range lines {
				seedAnsibleLogLine(
					t,
					f,
					deployment.ID,
					0,
					definition.Name,
					line,
				)
			}
			seedAnsibleLogLine(
				t,
				f,
				deployment.ID,
				1,
				"Script",
				heading,
			)
			scriptID := seedAnsibleLogLine(
				t,
				f,
				deployment.ID,
				1,
				"Script",
				lines[8],
			)
			b.wait(
				t,
				fmt.Sprintf(
					`document.querySelector('[data-log-id="%d"]')`,
					scriptID,
				),
			)
			assertAnsibleLogRendering(t, b)
			captureAnsibleLogs(t, b, fmt.Sprintf("ansible-live-%t", runbook))
			// Public JSON and plain-text exports retain exact, undecorated output.
			assertRawAnsibleLogs(
				t,
				f,
				apiPath,
				append([]string{heading, heading, lines[8]}, lines...),
			)
			b.navigateBackTest(t, f.baseURL+path)
			b.wait(
				t,
				`document.querySelectorAll('.ansible-log-line').length === 13`,
			)
			assertAnsibleLogRendering(t, b)
			captureAnsibleLogs(t, b, fmt.Sprintf("ansible-saved-%t", runbook))
		})
	}
}

func captureAnsibleLogs(t *testing.T, b *packageBrowser, name string) {
	t.Helper()
	b.evaluate(
		t,
		`document.querySelectorAll('details[data-step-index]').forEach(panel => panel.open = true); true`,
	)
	for _, theme := range []string{"light", "mocha"} {
		b.evaluate(
			t,
			fmt.Sprintf(
				`document.documentElement.dataset.theme = %q; true`,
				theme,
			),
		)
		for _, width := range []int{375, 768, 1280} {
			b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
				"width": width, "height": 1100, "deviceScaleFactor": 1, "mobile": false,
			}, &struct{}{})
			if string(b.evaluate(t, `(() => {
 const rows = [...document.querySelectorAll('.ansible-log-line:not(.ansible-log-blank)')];
 return document.documentElement.scrollWidth <= innerWidth && rows.every(row => {
 const timestamp = row.firstElementChild.getBoundingClientRect();
 const message = row.lastElementChild.getBoundingClientRect();
 return getComputedStyle(row).display === 'grid' && message.left > timestamp.right && message.width > 0;
 });
})()`)) != "true" {
				t.Fatal(
					"Ansible timestamps and wrapped output overlap or overflow",
				)
			}
			b.screenshot(t, fmt.Sprintf("%s-%s-%d", name, theme, width))
		}
	}
}

func assertRawAnsibleLogs(
	t *testing.T,
	f *artifactE2E,
	path string,
	expected []string,
) {
	t.Helper()
	var logs []db.DeploymentLog
	decodeLifecycleTest(t, f.api(t, "GET", path, nil, 200), &logs)
	if len(logs) != len(expected) {
		t.Fatal("API changed the number of log entries", len(logs))
	}
	counts := make(map[string]int)
	for _, log := range logs {
		counts[log.Line]++
	}
	exported := string(f.api(t, "GET", path+".txt", nil, 200))
	for _, line := range expected {
		if counts[line] == 0 || !strings.Contains(exported, line) {
			t.Fatalf("API or export changed raw log line %q", line)
		}
		counts[line]--
	}
}

func seedAnsibleLogLine(
	t *testing.T,
	f *artifactE2E,
	deploymentID, index int64,
	name, line string,
) int64 {
	t.Helper()
	row, err := f.h.repo.Queries.CreateStepDeploymentLog(
		t.Context(),
		db.CreateStepDeploymentLogParams{
			DeploymentID: deploymentID, StepIndex: sql.NullInt64{Int64: index, Valid: true},
			StepName: sql.NullString{String: name, Valid: true}, Line: line,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return row.ID
}

func assertAnsibleLogRendering(t *testing.T, b *packageBrowser) {
	t.Helper()
	b.wait(t, `document.querySelectorAll('.ansible-log-line').length === 13`)
	if string(b.evaluate(t, `(() => {
 const rows = [...document.querySelectorAll('.ansible-log-line')];
 const colored = (text, color) => rows.some(row => [...row.querySelectorAll('.'+color)].some(span => span.textContent === text));
 const time = rows[0].querySelector('.text-gray-500');
 const script = [...document.querySelectorAll('[data-log-id]')].at(-1);
 return colored('TASK [configure target]', 'text-info') && colored('PLAY RECAP', 'text-info') &&
 colored('ok: [target]', 'text-success') && colored('changed: [target]', 'text-warning') &&
 colored('skipping: [target]', 'text-info') && colored('[WARNING]: retained warning', 'text-warning') &&
 colored('fatal: [target]: UNREACHABLE!', 'text-error') &&
 [['ok=1','text-success'],['changed=1','text-warning'],['unreachable=1','text-error'],
  ['failed=1','text-error'],['skipped=1','text-info'],['rescued=1','text-warning'],['ignored=1','text-warning']]
 .every(([text,color]) => colored(text,color)) &&
 ['ok=0','changed=0','unreachable=0','failed=0','skipped=0','rescued=0','ignored=0']
 .every(text => colored(text,'ansible-recap-zero')) &&
 !rows.find(row => row.textContent.includes('ok=99')).querySelector('.text-success,.ansible-recap-zero') &&
 /^\[\d{2}:\d{2}:\d{2}\]$/.test(time.textContent) && /^\d{4}-\d{2}-\d{2} /.test(time.title) &&
 getComputedStyle(time).userSelect === 'none' && !document.querySelector('img[src=x]') &&
 rows.some(row => row.textContent.includes('<img src=x onerror=alert(1)> payload *****')) &&
 !script.classList.contains('ansible-log-line') && script.textContent.includes('target : ok=1 changed=1') &&
 !script.querySelector('.ansible-recap-zero,.text-success,.text-warning,.text-error,.text-info') &&
 new Set([...document.querySelectorAll('[data-log-id]')].map(row => row.dataset.logId)).size === 15;
})()`)) != "true" {
		t.Fatal(
			"Ansible live/saved formatting, raw script, or text safety differs",
		)
	}
}
