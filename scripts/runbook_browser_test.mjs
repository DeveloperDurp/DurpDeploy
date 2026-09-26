import assert from "node:assert/strict";
import { chromium } from "playwright";

const base = process.env.DURPDEPLOY_RUNBOOK_BROWSER_BASE;
const projectID = process.env.DURPDEPLOY_RUNBOOK_BROWSER_PROJECT_ID;
const runbookID = process.env.DURPDEPLOY_RUNBOOK_BROWSER_RUNBOOK_ID;
const scheduleID = process.env.DURPDEPLOY_RUNBOOK_BROWSER_SCHEDULE_ID;
const environmentID = process.env.DURPDEPLOY_RUNBOOK_BROWSER_ENVIRONMENT_ID;
const approvalProjectID = process.env.DURPDEPLOY_RUNBOOK_BROWSER_APPROVAL_PROJECT_ID;
const approvalEnvironments = [
  process.env.DURPDEPLOY_RUNBOOK_BROWSER_APPROVAL_DEV_ID,
  process.env.DURPDEPLOY_RUNBOOK_BROWSER_APPROVAL_STAGING_ID,
  process.env.DURPDEPLOY_RUNBOOK_BROWSER_APPROVAL_PROD_ID,
];
const apiToken = process.env.DURPDEPLOY_RUNBOOK_BROWSER_API_TOKEN;
const email = process.env.DURPDEPLOY_RUNBOOK_BROWSER_EMAIL;
const password = process.env.DURPDEPLOY_RUNBOOK_BROWSER_PASSWORD;

assert.ok(base && projectID && runbookID && scheduleID && environmentID && approvalProjectID && approvalEnvironments.every(Boolean) && apiToken && email && password);

const browser = await chromium.launch({ headless: true });
try {
  const page = await browser.newPage({ viewport: { width: 375, height: 812 } });
  const errors = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.goto(`${base}/login`);
  await page.getByLabel("Email").fill(email);
  await page.getByLabel("Password").fill(password);
  await page.getByRole("button", { name: "Login" }).click();
  await page.goto(`${base}/projects/${projectID}/runbooks/${runbookID}`);

  const mobile = page.locator(".md\\:hidden").filter({ hasText: "Next run:" });
  assert.equal(await mobile.isVisible(), true);
  assert.equal(await mobile.getByText("Cron:").count(), 1);
  assert.equal(await mobile.getByText("Version:").count(), 1);
  assert.equal(await mobile.getByText("Environment:").count(), 1);
  assert.match(await mobile.innerText(), /Pinned version 1/);
  assert.equal(await mobile.getByText("Next run:").count(), 1);
  assert.equal(await mobile.locator(`form[action$="/schedules/${scheduleID}/disable"]`).count(), 1);
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth), 375);

  await page.setViewportSize({ width: 1440, height: 900 });
  assert.equal(await mobile.isVisible(), false);
  const table = page.locator(".md\\:block table").filter({ hasText: "Next run" });
  assert.equal(await table.isVisible(), true);
  assert.equal(await table.locator(`form[action$="/schedules/${scheduleID}/disable"]`).count(), 1);
  await page.goto(`${base}/projects/${projectID}/runbooks/${runbookID}/edit`);
  await page.getByRole("button", { name: "Add step" }).click();
  const targets = page.locator('select[name="step_target"]');
  await targets.first().selectOption("agent");
  const selectors = page.locator('input[name="step_selectors"]:not([type="hidden"])');
  await selectors.first().fill("canary, production");
  await page.getByRole("button", { name: "Move step down" }).first().click();
  assert.equal(await selectors.nth(1).inputValue(), "canary, production");
  assert.equal(await targets.nth(1).inputValue(), "agent");
  await targets.nth(1).selectOption("local");
  await page.locator('input[name="step_name"]').first().fill("browser check");
  await page.locator('textarea[name="step_script"]').first().fill("printf browser-runbook-step; sleep 5");
  await page.getByRole("button", { name: "Save immutable version" }).click();
  await page.waitForURL(new RegExp(`/projects/${projectID}/runbooks/${runbookID}$`));
  assert.match(await page.locator("h2").allTextContents().then((values) => values.join(" ")), /Version 3 steps/);

  const executeForm = page.locator(`form[action$="/runbooks/${runbookID}/execute"]`);
  await executeForm.locator('select[name="environment_id"]').selectOption(environmentID);
  await executeForm.getByRole("button", { name: "Run book" }).click();
  await page.waitForURL(/\/runbooks\/executions\/\d+$/);
  await assert.doesNotReject(() => page.getByText("browser-runbook-step").first().waitFor({ timeout: 10000 }));
  assert.equal(await page.getByRole("button", { name: "Retry" }).count(), 0);
  await page.getByRole("button", { name: "Retry" }).waitFor({ timeout: 15000 });
  await page.getByRole("button", { name: "Retry" }).click();
  await page.waitForURL(/\/runbooks\/executions\/\d+$/);
  await page.goto(`${base}/projects/${projectID}/runbooks?offset=1`);
  assert.equal(await page.getByRole("columnheader", { name: "Created" }).count(), 2);
  await page.getByRole("link", { name: "Previous" }).click();
  await page.waitForURL(new RegExp(`/projects/${projectID}/runbooks\\?offset=0$`));
  await page.goto(`${base}/projects/${projectID}/runbooks/${runbookID}`);

  const scheduleForm = page.locator(`form[action$="/runbooks/${runbookID}/schedules"]`);
  await scheduleForm.locator('select[name="environment_id"]').selectOption(environmentID);
  await scheduleForm.locator('input[name="cron"]').fill("0 4 * * *");
  await scheduleForm.getByRole("button", { name: "Create schedule" }).click();
  await page.waitForURL(new RegExp(`/projects/${projectID}/runbooks/${runbookID}$`));
  const newSchedule = page.locator(".md\\:block tr").filter({ hasText: "0 4 * * *" });
  await newSchedule.getByRole("button", { name: "Disable" }).click();
  await page.waitForURL(new RegExp(`/projects/${projectID}/runbooks/${runbookID}$`));
  assert.match(await page.locator(".md\\:block tr").filter({ hasText: "0 4 * * *" }).innerText(), /Disabled/);

  const api = async (method, path, data, expectedStatus = 201) => {
    const response = await page.request.fetch(`${base}/api/v1${path}`, {
      method,
      headers: { Authorization: `Bearer ${apiToken}` },
      data,
    });
    assert.equal(response.status(), expectedStatus, await response.text());
    return response.json();
  };
  const waitForExecution = async (pid, id, desiredStatus) => {
    for (let attempt = 0; attempt < 100; attempt += 1) {
      const execution = await api("GET", `/projects/${pid}/runbook-executions/${id}`, undefined, 200);
      if (execution.status === desiredStatus) return;
      await new Promise((resolve) => setTimeout(resolve, 100));
    }
    assert.fail(`Runbook execution ${id} did not reach ${desiredStatus}`);
  };

  const longBook = await api("POST", `/projects/${projectID}/runbooks`, {
    name: "browser-cancel-check",
    steps: [{ name: "wait", script_body: "sleep 15", interpreter: "bash" }],
  });
  await page.goto(`${base}/projects/${projectID}/runbooks/${longBook.runbook.id}`);
  const longForm = page.locator(`form[action$="/runbooks/${longBook.runbook.id}/execute"]`);
  await longForm.locator('select[name="environment_id"]').selectOption(environmentID);
  await longForm.getByRole("button", { name: "Run book" }).click();
  await page.waitForURL(/\/runbooks\/executions\/\d+$/);
  const longExecutionID = Number(page.url().match(/\/executions\/(\d+)$/)?.[1]);
  await page.goto(`${base}/projects/${projectID}/edit`);
  page.once("dialog", (dialog) => dialog.accept());
  const blockedDelete = page.waitForResponse((response) =>
    response.url().endsWith(`/projects/${projectID}`) && response.request().method() === "DELETE");
  await page.getByRole("button", { name: "Delete project" }).click();
  assert.equal((await blockedDelete).status(), 409);
  await page.goto(`${base}/projects/${projectID}/runbooks/executions/${longExecutionID}`);
  await page.getByRole("button", { name: "Cancel" }).waitFor({ timeout: 10000 });
  await page.getByRole("button", { name: "Cancel" }).click();
  await waitForExecution(projectID, longExecutionID, "cancelled");
  await page.reload();
  assert.match(await page.locator("#status-badge").innerText(), /cancelled/);

  const approvalBook = await api("POST", `/projects/${approvalProjectID}/runbooks`, {
    name: "browser-approval-check",
    steps: [{ name: "check", script_body: "true", interpreter: "bash" }],
  });
  for (const envID of approvalEnvironments.slice(0, 2)) {
    const execution = await api("POST", `/projects/${approvalProjectID}/runbooks/${approvalBook.runbook.id}/executions`, { environment_id: Number(envID) });
    await waitForExecution(approvalProjectID, execution.id, "succeeded");
  }
  const approvalExecution = await api("POST", `/projects/${approvalProjectID}/runbooks/${approvalBook.runbook.id}/executions`, { environment_id: Number(approvalEnvironments[2]) });
  await page.goto(`${base}/projects/${approvalProjectID}/runbooks/executions/${approvalExecution.id}`);
  await page.getByRole("button", { name: "Approve" }).click();
  await waitForExecution(approvalProjectID, approvalExecution.id, "succeeded");
  assert.deepEqual(errors, []);
  console.log("Runbook browser save, execute, retry, schedule, disable, cancel and approve E2E: OK");
} finally {
  await browser.close();
}
