import assert from "node:assert/strict";
import { chromium } from "playwright";

const base = process.env.DURPDEPLOY_RUNBOOK_BROWSER_BASE;
const projectID = process.env.DURPDEPLOY_RUNBOOK_BROWSER_PROJECT_ID;
const runbookID = process.env.DURPDEPLOY_RUNBOOK_BROWSER_RUNBOOK_ID;
const scheduleID = process.env.DURPDEPLOY_RUNBOOK_BROWSER_SCHEDULE_ID;
const email = process.env.DURPDEPLOY_RUNBOOK_BROWSER_EMAIL;
const password = process.env.DURPDEPLOY_RUNBOOK_BROWSER_PASSWORD;

assert.ok(base && projectID && runbookID && scheduleID && email && password);

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
  assert.deepEqual(errors, []);
  console.log("Runbook mobile, desktop, and selector reorder browser E2E: OK");
} finally {
  await browser.close();
}
