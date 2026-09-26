import assert from "node:assert/strict";
import { chromium } from "playwright";

const base = process.env.DURPDEPLOY_RUNBOOK_BROWSER_BASE;
const projectID = process.env.DURPDEPLOY_RUNBOOK_BROWSER_PROJECT_ID;
const runbookID = process.env.DURPDEPLOY_RUNBOOK_BROWSER_RUNBOOK_ID;
const scheduleID = process.env.DURPDEPLOY_RUNBOOK_BROWSER_SCHEDULE_ID;
const environmentID = process.env.DURPDEPLOY_RUNBOOK_BROWSER_ENVIRONMENT_ID;
const email = process.env.DURPDEPLOY_RUNBOOK_BROWSER_EMAIL;
const password = process.env.DURPDEPLOY_RUNBOOK_BROWSER_PASSWORD;

assert.ok(base && projectID && runbookID && scheduleID && environmentID && email && password);

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
  await page.locator('input[name="step_name"]').nth(1).fill("browser check");
  await page.locator('textarea[name="step_script"]').nth(1).fill("printf browser-runbook-step");
  await page.getByRole("button", { name: "Save immutable version" }).click();
  await page.waitForURL(new RegExp(`/projects/${projectID}/runbooks/${runbookID}$`));
  assert.match(await page.locator("h2").allTextContents().then((values) => values.join(" ")), /Version 3 steps/);

  const executeForm = page.locator(`form[action$="/runbooks/${runbookID}/execute"]`);
  await executeForm.locator('select[name="environment_id"]').selectOption(environmentID);
  await executeForm.getByRole("button", { name: "Run book" }).click();
  await page.waitForURL(/\/runbooks\/executions\/\d+$/);
  await assert.doesNotReject(() => page.getByText("browser-runbook-step").first().waitFor({ timeout: 10000 }));
  await page.reload();
  await page.getByRole("button", { name: "Retry" }).waitFor({ timeout: 10000 });
  await page.getByRole("button", { name: "Retry" }).click();
  await page.waitForURL(/\/runbooks\/executions\/\d+$/);
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
  assert.deepEqual(errors, []);
  console.log("Runbook mobile, desktop, save, execute, retry, schedule and disable browser E2E: OK");
} finally {
  await browser.close();
}
