import { promises as fs } from "node:fs";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { execFile } from "node:child_process";
import { promisify } from "node:util";

const execFileAsync = promisify(execFile);
const nodeModules =
  process.env.MOBILE_BROWSER_NODE_MODULES || join(process.cwd(), "node_modules");
const require = createRequire(join(nodeModules, "package.json"));
const { chromium } = require("playwright");

function validateBaseURL(value) {
  let url;
  try {
    url = new URL(value);
  } catch {
    throw new Error("BASE_URL must use a localhost origin");
  }
  const localHosts = new Set(["localhost", "127.0.0.1", "::1", "[::1]"]);
  if (
    (url.protocol !== "http:" && url.protocol !== "https:") ||
    !localHosts.has(url.hostname)
  ) {
    throw new Error("BASE_URL must use a localhost origin");
  }
  return url.origin;
}

if (process.env.MOBILE_BASE_URL_SELF_TEST === "1") {
  validateBaseURL("https://example.test");
  throw new Error("remote BASE_URL was not rejected");
}

const diagnosticBaseline =
  process.env.MOBILE_BASELINE === "1" && process.env.MOBILE_STRICT !== "1";

if (process.env.MOBILE_GEOMETRY_SELF_TEST === "1") {
	assertGeometry(
    { name: "self-test" },
    { width: 375 },
    {
      surfaceFound: true,
      rowFound: true,
      clientWidth: 375,
      documentWidth: 375,
      bodyWidth: 375,
      controlRects: [{ left: 0, right: 40 }, { left: 340, right: 400 }],
    },
  );
	throw new Error("second off-screen control was not detected");
}

if (process.env.MOBILE_MISSING_SELECTOR_SELF_TEST === "1") {
  assertGeometry(
    { name: "missing-selector" },
    { name: "self-test", width: 375, layout: "mobile" },
    { surfaceFound: true, rowFound: false },
  );
  throw new Error("missing selector was not detected");
}

const required = [
  "BASE_URL",
  "MOBILE_ROLE",
  "MOBILE_COOKIE_NAME",
  "MOBILE_COOKIE_VALUE",
  "MOBILE_PROJECT_ID",
  "MOBILE_LIFECYCLE_ID",
  "MOBILE_STEP_ID",
  "MOBILE_TEMPLATE_ID",
  "MOBILE_SCHEDULE_ID",
  "MOBILE_VARIABLE_ID",
  "MOBILE_SECRET_VARIABLE_ID",
  "MOBILE_SECRET_SENTINEL",
  "MOBILE_OUTPUT_DIR",
];

for (const name of required) {
  if (!process.env[name]) {
    throw new Error(`missing ${name}`);
  }
}

const config = Object.freeze({
  baseURL: validateBaseURL(process.env.BASE_URL),
  role: process.env.MOBILE_ROLE,
  cookie: { name: process.env.MOBILE_COOKIE_NAME, value: process.env.MOBILE_COOKIE_VALUE },
  projectID: process.env.MOBILE_PROJECT_ID,
  lifecycleID: process.env.MOBILE_LIFECYCLE_ID,
  stepID: process.env.MOBILE_STEP_ID,
  templateID: process.env.MOBILE_TEMPLATE_ID,
  scheduleID: process.env.MOBILE_SCHEDULE_ID,
  variableID: process.env.MOBILE_VARIABLE_ID,
  secretVariableID: process.env.MOBILE_SECRET_VARIABLE_ID,
  secretSentinel: process.env.MOBILE_SECRET_SENTINEL,
  outputDir: process.env.MOBILE_OUTPUT_DIR,
});

const targetNames = new Set(
  (process.env.MOBILE_TARGETS ?? "").split(",").filter(Boolean),
);

const viewportNames = new Set(
  (process.env.MOBILE_VIEWPORTS ?? "").split(",").filter(Boolean),
);

const allViewports = [
  { name: "phone", width: 375, height: 812, layout: "mobile" },
  { name: "tablet", width: 768, height: 1024, layout: "native" },
  { name: "desktop", width: 1280, height: 768, layout: "native" },
];

const unknownViewportNames = [...viewportNames].filter(
  (viewportName) =>
    !allViewports.some((viewport) => viewport.name === viewportName),
);

if (
  unknownViewportNames.length > 0 ||
  (process.env.MOBILE_VIEWPORTS !== undefined && viewportNames.size === 0)
) {
  const invalidViewportSelection =
    unknownViewportNames.length > 0
      ? unknownViewportNames.join(",")
      : "empty";
  throw new Error(
    `invalid mobile readability viewport selection: ${invalidViewportSelection}`,
  );
}

const allTargets = [
  {
    name: "navbar",
    path: "/",
    surface: "body",
    mobileRow: "[data-mobile-nav] > summary",
    desktopRow: "[data-mobile-nav] + div > ul",
    controls: "[data-mobile-nav] > summary",
    mobileControls: "[data-mobile-nav] > summary",
    desktopControls: "[data-mobile-nav] + div > ul > li > a",
    readOnlyControls: true,
  },
  {
    name: "steps",
    path: `/projects/${config.projectID}/steps-page`,
    surface: "#step-list",
    row: `#step-row-${config.stepID}`,
    mobileRow: `[data-mobile-step="${config.stepID}"]`,
    controls: `#step-row-${config.stepID} [data-step-action]`,
    mobileControls: `[data-mobile-step="${config.stepID}"] [data-step-action]`,
    actionAttribute: "data-step-action",
    writerActions: ["move-down", "edit", "delete", "save-template"],
    writerControlCount: 4,
    desktopTable: "#step-list > table",
    mobileRecord: "[data-mobile-step-list]",
  },
  {
    name: "lifecycle-stages",
    path: `/lifecycles/${config.lifecycleID}`,
    surface: "#lifecycle-stages",
    row: "#lifecycle-stages > table",
    mobileRow: "[data-mobile-lifecycle-stage-list] > [data-mobile-lifecycle-stage]:first-child",
    controls: "#lifecycle-stages > table tbody > tr:first-child [data-lifecycle-stage-action]",
    mobileControls: "[data-mobile-lifecycle-stage-list] > [data-mobile-lifecycle-stage]:first-child [data-lifecycle-stage-action]",
    actionAttribute: "data-lifecycle-stage-action",
    writerActions: ["approval", "move-down", "delete"],
    writerControlCount: 3,
    desktopTable: "#lifecycle-stages > table",
    mobileRecord: "[data-mobile-lifecycle-stage-list]",
  },
  {
    name: "schedules",
    path: `/projects/${config.projectID}/schedules`,
    surface: "#schedules-content",
    row: `#schedule-row-${config.scheduleID}`,
    mobileRow: `[data-mobile-schedule="${config.scheduleID}"]`,
    controls: `#schedule-row-${config.scheduleID} [data-schedule-action]`,
    mobileControls: `[data-mobile-schedule="${config.scheduleID}"] [data-schedule-action]`,
    actionAttribute: "data-schedule-action",
    writerActions: ["edit", "toggle", "delete"],
    writerControlCount: 3,
    desktopTable: "#schedules-content > table",
    mobileRecord: "[data-mobile-schedule-list]",
  },
  {
    name: "variables",
    path: `/projects/${config.projectID}/variables`,
    surface: "#variables-content",
    row: `#variable-row-${config.variableID}`,
    mobileRow: `[data-mobile-variable="${config.variableID}"]`,
    controls: `#variable-row-${config.variableID} [data-variable-action]`,
    mobileControls: `[data-mobile-variable="${config.variableID}"] [data-variable-action]`,
    actionAttribute: "data-variable-action",
    writerActions: ["edit", "delete"],
    writerControlCount: 2,
    desktopTable: "#variables-content > table",
    mobileRecord: "[data-mobile-variable-list]",
  },
  {
    name: "templates",
    path: "/templates",
    surface: "#templates-content",
    row: "#templates-list tbody > tr",
    controls: "#templates-list tbody > tr button",
    writerControlCount: 1,
    disclosure: `[data-disclosure="template-script-${config.templateID}"]`,
  },
  {
    name: "template-history",
    path: `/templates/${config.templateID}/history`,
    surface: "#template-history",
    row: "#template-history tbody > tr",
    controls: "#template-history button",
    disclosure: "[data-disclosure=\"template-history-script\"]",
  },
  {
    name: "projects",
    path: "/projects",
    surface: "#projects-list",
    row: "#projects-list > div > table > tbody > tr",
    controls: "#projects-list button",
    scrollContainer: "[data-project-environment-scroll]",
  },
  {
    name: "audit",
    path: "/admin/audit",
    surface: "#form-container",
    row: "#form-container tbody > tr",
    rowText: "hostile.audit",
    controls: "#form-container button",
    disclosure: "[data-disclosure=\"audit-details\"]",
    disclosureRowName: /hostile\.audit/,
    disclosureContent: "audit-token",
    roles: ["admin"],
  },
];

const unknownTargetNames = [...targetNames].filter(
  (targetName) =>
    !allTargets.some((target) => target.name === targetName),
);

if (unknownTargetNames.length > 0) {
  throw new Error(
    `unknown mobile readability target: ${unknownTargetNames.join(", ")}`,
  );
}

const targets = allTargets.filter(
  (target) =>
    (targetNames.size === 0 || targetNames.has(target.name)) &&
    (!target.roles || target.roles.includes(config.role)),
);

const viewports = allViewports.filter(
  (viewport) => viewportNames.size === 0 || viewportNames.has(viewport.name),
);

if (viewports.length === 0) {
  throw new Error("invalid mobile readability viewport selection: empty");
}

let profile;
let browser;
let cleanup = newCleanupReceipt();
let report;
let cleanupPromise;
let ownedProcessGroups = new Map();
let browserAttempts = 0;
let signalName;
let resolveSignal;
const signalReceived = new Promise((resolve) => {
  resolveSignal = resolve;
});

process.on("SIGINT", () => handleSignal("SIGINT"));
process.on("SIGTERM", () => handleSignal("SIGTERM"));

try {
  await fs.mkdir(config.outputDir, { recursive: true });
  const measurements = await runBrowserMatrixWithRetry();
  report = { role: config.role, attempts: browserAttempts, measurements };
} finally {
  await cleanupResources();
}

async function runBrowserMatrixWithRetry() {
  for (let attempt = 1; attempt <= 2; attempt += 1) {
    try {
      const context = await launchBrowser();
      browserAttempts = attempt;
      if (process.env.MOBILE_HOLD_FOR_SIGNAL === "1") {
        await signalReceived;
      }
      if (signalName) {
        return [];
      }
      if (
        process.env.MOBILE_FORCE_CONTEXT_CLOSE_ONCE === "1" &&
        attempt === 1
      ) {
        await browser.close();
      }
      return await inspectBrowser(context);
    } catch (error) {
      const shouldRetry =
        !signalName &&
        attempt < 2 &&
        isUnexpectedBrowserClosure(error);
      try {
        await cleanupResources();
      } catch (cleanupError) {
        throw new AggregateError(
          [error, cleanupError],
          "mobile browser run and cleanup failed",
        );
      }
      if (signalName) {
        return [];
      }
      if (!shouldRetry) {
        throw error;
      }
      resetBrowserRun();
    }
  }
  throw new Error("mobile browser retry loop ended unexpectedly");
}

async function launchBrowser() {
  profile = await fs.mkdtemp(join(tmpdir(), "durpdeploy-mobile-chromium-"));
  const executablePath = chromium.executablePath();
  await fs.access(executablePath);
  const context = await chromium.launchPersistentContext(profile, {
    headless: true,
    executablePath,
    handleSIGINT: false,
    handleSIGTERM: false,
  });
  browser = context.browser();
  if (!browser) {
    throw new Error("Playwright Chromium has no browser connection");
  }
  await recordProfileProcessTrees();
  await context.addCookies([{ ...config.cookie, url: config.baseURL }]);
  if (process.env.MOBILE_READY_FILE) {
    await fs.writeFile(process.env.MOBILE_READY_FILE, profile);
  }
  if (process.env.MOBILE_PROFILE_RECEIPT_FILE) {
    await fs.writeFile(process.env.MOBILE_PROFILE_RECEIPT_FILE, profile);
  }
  return context;
}

async function inspectBrowser(context) {
  const measurements = [];
  const interactionFailures = [];
  for (const viewport of viewports) {
    const page = await context.newPage();
    await page.setViewportSize(viewport);
    for (const target of targets) {
      const response = await page.goto(`${config.baseURL}${target.path}`, { waitUntil: "networkidle" });
      if (!response || response.status() !== 200) {
        throw new Error(`${target.path} returned ${response?.status() ?? "no response"}`);
      }
      const mobileLayout = viewport.layout === "mobile";
      const row = mobileLayout
        ? target.mobileRow ?? target.row
        : target.desktopRow ?? target.row;
      const controls = mobileLayout
        ? target.mobileControls ?? target.controls
        : target.desktopControls ?? target.controls;
      const writerControls = target.readOnlyControls
        ? null
        : target.writerControls ?? controls;
      const surface = page.locator(target.surface);
      if ((await surface.count()) !== 1) {
        throw new Error(`${target.name} surface is missing or ambiguous`);
      }
      await surface.waitFor({ state: "visible" });
      const rowLocator = target.rowText
        ? page.locator(row).filter({ hasText: target.rowText })
        : page.locator(row);
      if ((await rowLocator.count()) !== 1) {
        throw new Error(
          `${target.name} row is missing or ambiguous at ${viewport.width}px`,
        );
      }
      await rowLocator.waitFor({ state: "visible" });
      const disclosure = await assertDisclosure(page, target);
      const scrollContainer = await assertScrollContainer(page, target);
      const geometry = await page.evaluate(({ surface, row, rowText, controls, writerControls, desktopTable, mobileRecord, actionAttribute }) => {
        const clientWidth = document.documentElement.clientWidth;
        const documentWidth = document.documentElement.scrollWidth;
        const bodyWidth = document.body.scrollWidth;
        const rowElements = Array.from(document.querySelectorAll(row)).filter(
          (element) => !rowText || element.textContent?.includes(rowText),
        );
        const rowElement = rowElements.length === 1 ? rowElements[0] : null;
        const desktopTableElement = desktopTable && document.querySelector(desktopTable);
        const mobileRecordElement = mobileRecord && document.querySelector(mobileRecord);
       const controlElements = Array.from(document.querySelectorAll(controls));
       const writerControlElements = writerControls
         ? Array.from(document.querySelectorAll(writerControls))
         : [];
       const controlRects = controlElements.map((element) => {
         const rect = element.getBoundingClientRect();
         return { left: rect.left, right: rect.right, top: rect.top, bottom: rect.bottom };
       });
		 const writerControlRects = writerControlElements.map((element) => {
			 const rect = element.getBoundingClientRect();
			 return { left: rect.left, right: rect.right, top: rect.top, bottom: rect.bottom };
		 });
         const rowRect = rowElement?.getBoundingClientRect();
		 const overflowingElements = Array.from(document.querySelectorAll("*")).flatMap((element) => {
			const rect = element.getBoundingClientRect();
			if (rect.right <= clientWidth && rect.left >= 0) {
				return [];
			}
			return [{ tagName: element.tagName, className: element.className }];
		 });
         return {
          clientWidth,
          documentWidth,
          bodyWidth,
          surfaceFound: document.querySelector(surface) !== null,
          rowFound: rowElement !== null,
          desktopTableVisible: desktopTableElement
            ? getComputedStyle(desktopTableElement).display !== "none"
            : true,
          mobileRecordFound: mobileRecordElement !== null,
          mobileRecordVisible: mobileRecordElement
            ? getComputedStyle(mobileRecordElement).display !== "none"
            : false,
          rowRect: rowRect && { left: rowRect.left, right: rowRect.right },
          controlCount: controlRects.length,
           visibleControlCount: controlRects.filter(
             (rect) => rect.right > rect.left && rect.bottom > rect.top,
           ).length,
           controlActions: controlElements.map((element) =>
              element.getAttribute(actionAttribute),
           ).filter(Boolean),
			 writerControlCount: writerControlRects.length,
			 visibleWriterControlCount: writerControlRects.filter(
				 (rect) => rect.right > rect.left && rect.bottom > rect.top,
			 ).length,
			 writerControlActions: writerControlElements.map((element) =>
				 element.getAttribute(actionAttribute),
			 ).filter(Boolean),
				overflowingElements,
            controlRects,
         };
      }, { ...target, row, controls, writerControls });
      const violations = assertGeometry(target, viewport, geometry);
      const interactionFailure = await mobileInteractionFailure(page, target, viewport);
      if (interactionFailure) {
        interactionFailures.push(interactionFailure);
      }
		const secretMask = await assertSecretMask(page, target);
      const screenshot = join(config.outputDir, `${config.role}-${target.name}-${viewport.width}.png`);
      await page.screenshot({ path: screenshot, fullPage: true });
		const screenshotSentinelAbsent = !(
			await fs.readFile(screenshot)
		).includes(Buffer.from(config.secretSentinel));
		if (!screenshotSentinelAbsent) {
			await fs.rm(screenshot, { force: true });
			throw new Error(`${target.name} screenshot contains the secret sentinel`);
		}
		measurements.push({ target: target.name, path: target.path, viewport, disclosure, scrollContainer, geometry, interactionFailure, secretMask, violations, screenshotSentinelAbsent });
    }
    await page.close();
  }
  if (interactionFailures.length > 0 && !diagnosticBaseline) {
    throw new Error(interactionFailures.join("; "));
  }
  return measurements;
}

function resetBrowserRun() {
  profile = undefined;
  browser = undefined;
  cleanup = newCleanupReceipt();
  cleanupPromise = undefined;
  ownedProcessGroups = new Map();
}

function isUnexpectedBrowserClosure(error) {
  const message = error instanceof Error ? error.message : String(error);
  return (
    /Target page, context or browser has been closed|Target closed|Browser has been closed|Connection closed/i.test(message) ||
    Boolean(browser && !browser.isConnected())
  );
}

function handleSignal(signal) {
  if (signalName) {
    return;
  }
  signalName = signal;
  process.exitCode = signal === "SIGINT" ? 130 : 143;
  resolveSignal(signal);
  const cleanupKeepAlive = setInterval(() => {}, 1_000);
  void cleanupResources().then(
    () => clearInterval(cleanupKeepAlive),
    (error) => {
      clearInterval(cleanupKeepAlive);
      console.error(error);
      process.exitCode = 1;
    },
  );
}

function cleanupResources() {
  if (!cleanupPromise) {
    cleanupPromise = runCleanup();
  }
  return cleanupPromise;
}

async function runCleanup() {
  const errors = [];
  await cleanupPhase("browser", async () => {
    await disconnectBrowser();
  }, errors);
  await cleanupPhase("discover owned process groups", async () => {
    if (profile) {
      await recordProfileProcessTrees();
    }
  }, errors);
  await cleanupPhase("profile processes", async () => {
    if (profile) {
      await stopProfileProcesses();
      cleanup.profileProcessesExited = true;
    }
  }, errors);
  await cleanupPhase("process groups", async () => {
    if (profile) {
      await waitForOwnedProcessGroupsExit();
      cleanup.processGroupsExited = true;
    }
  }, errors);
  await cleanupPhase("profile", async () => {
    if (profile) {
      await removeProfile(profile);
      cleanup.profileRemoved = true;
    }
  }, errors);
  await cleanupPhase("final OS state", async () => {
    if (profile) {
      await waitForProfileProcesses(5_000);
      await waitForOwnedProcessGroupsExit();
      if (
        !cleanup.browserDisconnected ||
        !cleanup.profileProcessesExited ||
        !cleanup.processGroupsExited ||
        !cleanup.profileRemoved
      ) {
        throw new Error("owned browser cleanup receipt is incomplete");
      }
      cleanup.processExited = true;
    }
  }, errors);
  if (errors.length > 0) {
    throw new AggregateError(errors, "mobile browser cleanup failed");
  }
  if (report) {
    await fs.writeFile(
      join(config.outputDir, `${config.role}.json`),
      `${JSON.stringify({ ...report, cleanup }, null, 2)}\n`,
    );
  }
}

async function cleanupPhase(name, action, errors) {
  try {
    await action();
  } catch (error) {
    errors.push(new Error(`${name}: ${error.message}`, { cause: error }));
  }
}

function assertGeometry(target, viewport, geometry) {
  const violations = [];
	const mobileLayout = viewport.layout === "mobile";
  if (!geometry.surfaceFound || !geometry.rowFound) {
    throw new Error(`${target.name} marker is missing at ${viewport.width}px`);
  }
  if (target.mobileRecord && mobileLayout) {
    if (!geometry.mobileRecordFound || !geometry.mobileRecordVisible) {
      violations.push(`${target.name} mobile record is missing at ${viewport.width}px`);
    }
    if (geometry.desktopTableVisible) {
      violations.push(`${target.name} desktop table is visible at ${viewport.width}px`);
    }
  }
  if (target.mobileRecord && !mobileLayout) {
    if (geometry.mobileRecordVisible) {
      violations.push(`${target.name} mobile record is visible at ${viewport.width}px`);
    }
    if (!geometry.desktopTableVisible) {
      violations.push(`${target.name} desktop table is hidden at ${viewport.width}px`);
    }
  }
  if (geometry.documentWidth > geometry.clientWidth || geometry.bodyWidth > geometry.clientWidth) {
    violations.push(`${target.name} overflows at ${viewport.width}px`);
  }
  for (const rect of geometry.controlRects) {
    if (rect.left < 0 || rect.right > geometry.clientWidth) {
      violations.push(`${target.name} control is unreachable at ${viewport.width}px`);
    }
  }
  if (
    process.env.MOBILE_ROLE === "viewer" &&
    geometry.visibleWriterControlCount > 0
  ) {
    violations.push(`${target.name} exposes controls to a viewer at ${viewport.width}px`);
  }
  if (
    process.env.MOBILE_ROLE !== "viewer" &&
    target.writerControlCount !== undefined &&
    geometry.visibleWriterControlCount !== target.writerControlCount
  ) {
    violations.push(
      `${target.name} has ${geometry.visibleWriterControlCount} visible writer controls at ${viewport.width}px, want ${target.writerControlCount}`,
    );
  }
  if (
    process.env.MOBILE_ROLE !== "viewer" &&
    target.writerActions &&
    geometry.writerControlActions.join(",") !== target.writerActions.join(",")
  ) {
    violations.push(
      `${target.name} actions = ${geometry.writerControlActions.join(",")}, want ${target.writerActions.join(",")}`,
    );
  }
  if (
    violations.length > 0 &&
    !diagnosticBaseline
  ) {
    throw new Error(violations.join("; "));
  }
  return violations;
}

async function mobileInteractionFailure(page, target, viewport) {
	if (target.name === "navbar") {
		try {
			const menu = page.locator("[data-mobile-nav]");
			if (viewport.layout === "mobile") {
				const summary = page.locator("[data-mobile-nav] > summary");
				const panel = page.locator("[data-mobile-nav] > ul");
				const themeToggle = page.locator(
					"[data-mobile-nav] > ul > li > button",
				);
				if (
					(await menu.count()) !== 1 ||
					(await summary.count()) !== 1 ||
					(await panel.count()) !== 1 ||
					(await themeToggle.count()) !== 1
				) {
					return "navbar mobile controls are missing or ambiguous";
				}
				if (!(await summary.isVisible())) {
					return "navbar mobile disclosure is not visible";
				}
				await summary.focus();
				await summary.press("Space");
				await panel.waitFor({ state: "visible", timeout: 2_000 });
				if (!(await themeToggle.isVisible())) {
					return "navbar mobile theme control is not visible";
				}
				const beforeTheme = await page
					.locator("html")
					.getAttribute("data-theme");
				await themeToggle.click();
				const afterTheme = await page
					.locator("html")
					.getAttribute("data-theme");
				const bounds = await panel.evaluate((element) => {
					const rect = element.getBoundingClientRect();
					return { left: rect.left, right: rect.right, width: innerWidth };
				});
				if (
					beforeTheme === afterTheme ||
					bounds.left < 0 ||
					bounds.right > bounds.width
				) {
					return "navbar menu did not toggle the theme or stay right-aligned";
				}
				await writeNavbarScreenshot(page, viewport, "open");
				await page.keyboard.press("Escape");
				if (await menu.evaluate((element) => element.open)) {
					return "navbar menu did not close with Escape";
				}
				await summary.click();
				await page.mouse.click(0, 200);
				if (await menu.evaluate((element) => element.open)) {
					return "navbar menu did not close on outside click";
				}
				return null;
			}
			if ((await menu.count()) !== 1) {
				return "navbar mobile disclosure is missing at native width";
			}
			if (await menu.isVisible()) {
				return "navbar mobile menu is visible at native width";
			}
			const nativeNavigation = page.locator("[data-mobile-nav] + div > ul");
			if (
				(await nativeNavigation.count()) !== 1 ||
				!(await nativeNavigation.isVisible())
			) {
				return "navbar native navigation is not visible at native width";
			}
			if ((await nativeNavigation.locator(":scope > li > a").count()) < 5) {
				return "navbar native navigation is missing read links";
			}
			const accountMenu = page.locator("[data-account-menu]:visible");
			if ((await accountMenu.count()) !== 1) {
				return "navbar desktop account menu is missing or ambiguous";
			}
			await accountMenu.locator(":scope > summary").click();
			const visibleLogoutCount = await accountMenu
				.locator('form[action="/logout"]')
				.evaluateAll(
					(elements) =>
						elements.filter((element) => element.checkVisibility()).length,
				);
			if (visibleLogoutCount !== 1) {
				return "navbar desktop logout is not visible exactly once";
			}
			await page.keyboard.press("Escape");
			await writeNavbarScreenshot(page, viewport, "desktop");
			return null;
		} catch (error) {
			return `navbar interaction did not complete: ${error instanceof Error ? error.message : String(error)}`;
		}
	}
		if (
		(target.name === "schedules" || target.name === "variables") &&
		viewport.width >= 1024 &&
		config.role !== "viewer"
	) {
		try {
			const action = target.name === "schedules" ? "schedule" : "variable";
			const id = target.name === "schedules" ? config.scheduleID : config.variableID;
			const rowSelector = `#${action}-row-${id}`;
			const row = page.locator(rowSelector);
			const edit = row.locator(`[data-${action}-action="edit"]`);
			if (await edit.getAttribute("hx-target") !== rowSelector) {
				return `${target.name} desktop edit changed its stable row target`;
			}
			await edit.click();
			await page.waitForLoadState("networkidle");
			const editor = page.locator(`${rowSelector} form`);
			await editor.waitFor({ state: "visible", timeout: 2_000 });
			return null;
		} catch {
			return `${target.name} desktop edit did not render a visible replacement form`;
		}
	}
  if (target.name === "steps" && viewport.width >= 1024 && config.role !== "viewer") {
    try {
      const row = page.locator(`#step-row-${config.stepID}`);
      await row.evaluate((element) => {
        element.setAttribute("data-desktop-interaction-probe", "before");
      });
      await row.locator('[data-step-action="edit"]').click();
      const editor = page.locator(`#step-row-${config.stepID} form`);
      await editor.waitFor({ state: "visible", timeout: 2_000 });
      if (await row.getAttribute("data-desktop-interaction-probe") === "before") {
        return "steps desktop edit did not swap the visible table row";
      }
      return null;
    } catch {
      return "steps desktop edit did not render a visible table-row form";
    }
  }
  if (viewport.name !== "phone" || config.role === "viewer") {
    return null;
  }
  if (target.name === "steps") {
    try {
      const record = page.locator(`[data-mobile-step="${config.stepID}"]`);
      await record.locator('[data-step-action="edit"]').click();
      const editor = record.locator(`[data-mobile-step-editor="${config.stepID}"] form`);
      await editor.waitFor({ state: "visible", timeout: 2_000 });
      return null;
    } catch {
      return "steps mobile edit did not render a visible mobile form";
    }
  }
	if (target.name === "schedules" || target.name === "variables") {
		try {
			const action = target.name === "schedules" ? "schedule" : "variable";
			const id = target.name === "schedules" ? config.scheduleID : config.variableID;
			const record = page.locator(`[data-mobile-${action}="${id}"]`);
			const edit = record.locator(`[data-${action}-action="edit"]`);
			if (await edit.getAttribute("hx-target") !== `#${action}-row-${id}`) {
				return `${target.name} mobile edit changed its stable row target`;
			}
			await edit.click();
			await page.waitForLoadState("networkidle");
			const editor = record.locator(`[data-mobile-${action}-edit] form`);
			await editor.waitFor({ state: "visible", timeout: 2_000 });
			return null;
		} catch {
			return `${target.name} mobile edit did not render a visible mobile form`;
		}
	}
	if (target.name !== "lifecycle-stages") {
    return null;
  }

  const failures = [];
  const records = page.locator(
    "[data-mobile-lifecycle-stage-list] > [data-mobile-lifecycle-stage]",
  );
  if (await records.count() < 2) {
    failures.push("lifecycle mobile fixture has fewer than two stages");
  } else {
    const firstActions = await records.nth(0).locator("[data-lifecycle-stage-action]").evaluateAll(
      (elements) => elements.map((element) => element.getAttribute("data-lifecycle-stage-action")),
    );
    const secondActions = await records.nth(1).locator("[data-lifecycle-stage-action]").evaluateAll(
      (elements) => elements.map((element) => element.getAttribute("data-lifecycle-stage-action")),
    );
    const firstID = await records.nth(0).getAttribute("data-mobile-lifecycle-stage");
    const secondID = await records.nth(1).getAttribute("data-mobile-lifecycle-stage");
    const firstReorderID = await records.nth(0).locator('form:has([data-lifecycle-stage-action="move-down"]) input[name="stage_id"]').getAttribute("value");
    const secondReorderID = await records.nth(1).locator('form:has([data-lifecycle-stage-action="move-up"]) input[name="stage_id"]').getAttribute("value");
    if (!firstActions.includes("move-down") || !secondActions.includes("move-up") || firstReorderID !== firstID || secondReorderID !== secondID) {
      failures.push("lifecycle mobile reorder markers are missing");
    }
  }

  try {
    await page.locator("#lifecycle-stages").evaluate((element) => {
      element.setAttribute("data-mobile-interaction-probe", "before");
    });
    const approval = records.nth(0).locator('[data-lifecycle-stage-action="approval"]');
    const wasChecked = await approval.isChecked();
    await approval.click();
    await page.waitForFunction(
      () => document.querySelector("#lifecycle-stages")?.getAttribute("data-mobile-interaction-probe") !== "before",
      undefined,
      { timeout: 2_000 },
    );
    if (
      await records
        .nth(0)
        .locator('[data-lifecycle-stage-action="approval"]')
        .isChecked() === wasChecked
    ) {
      failures.push("lifecycle mobile approval did not show the updated state");
    }
  } catch {
    failures.push("lifecycle mobile approval did not swap the visible lifecycle region");
  }
  return failures.length > 0 ? failures.join(", ") : null;
}

async function writeNavbarScreenshot(page, viewport, state) {
	if (!process.env.MOBILE_DURABLE_EVIDENCE_DIR) {
		return;
	}
	await fs.mkdir(process.env.MOBILE_DURABLE_EVIDENCE_DIR, { recursive: true });
	await page.screenshot({
		path: join(
			process.env.MOBILE_DURABLE_EVIDENCE_DIR,
			`navbar-${config.role}-${viewport.width}-${state}.png`,
		),
		fullPage: true,
	});
}

async function assertSecretMask(page, target) {
	if (target.name !== "variables") {
		return null;
	}
	const state = await page.evaluate(({ secretVariableID, secretSentinel }) => {
		const record = document.querySelector(`[data-mobile-variable="${secretVariableID}"]`);
		const html = document.documentElement.outerHTML;
		const text = document.body.textContent ?? "";
		const mask = Array.from(document.querySelectorAll("span")).find(
			(element) => element.textContent?.includes("••••••••") && element.getBoundingClientRect().width > 0,
		);
		return {
			htmlAbsent: !html.includes(secretSentinel),
			textAbsent: !text.includes(secretSentinel),
			maskVisible: record !== null && mask !== undefined,
		};
	}, { secretVariableID: config.secretVariableID, secretSentinel: config.secretSentinel });
	if (!state.htmlAbsent || !state.textAbsent || !state.maskVisible) {
		throw new Error("variables secret mask is missing or the sentinel is exposed");
	}
	return state;
}

async function assertDisclosure(page, target) {
  if (!target.disclosure) {
    return null;
  }
  const row = target.disclosureRowName
    ? page.getByRole("row", { name: target.disclosureRowName })
    : null;
  if (row && (await row.count()) !== 1) {
    throw new Error(`${target.name} disclosure row is missing or ambiguous`);
  }
  const details = row ? row.locator(target.disclosure) : page.locator(target.disclosure);
  if (await details.count() !== 1) {
    throw new Error(`${target.name} native disclosure is missing`);
  }
  const summary = details.locator(":scope > summary");
  if (await summary.count() !== 1) {
    throw new Error(`${target.name} disclosure summary is missing`);
  }
  await summary.focus();
  await summary.press("Space");
  const state = await details.evaluate((element, disclosureContent) => {
    const summaryElement = element.querySelector("summary");
    const code = element.querySelector("code");
    const text = code?.textContent ?? "";
    const rect = code?.getBoundingClientRect();
    return {
      focused: document.activeElement === summaryElement,
      focusVisible: summaryElement?.matches(":focus-visible") ?? false,
      open: element.open,
      textLength: text.length,
      contentMatches: !disclosureContent || text.includes(disclosureContent),
      contentVisible: Boolean(rect && rect.width > 0 && rect.height > 0),
    };
  }, target.disclosureContent);
  if (!state.focused || !state.focusVisible || !state.open || !state.contentMatches || !state.contentVisible || state.textLength < 200) {
    throw new Error(`${target.name} disclosure cannot reveal hostile content`);
  }
  return state;
}

async function assertScrollContainer(page, target) {
  if (!target.scrollContainer) {
    return null;
  }
  const container = page.locator(target.scrollContainer);
  if (await container.count() !== 1) {
    throw new Error(`${target.name} scoped table scroll container is missing`);
  }
  const overflowX = await container.evaluate((element) => getComputedStyle(element).overflowX);
  if (overflowX !== "auto" && overflowX !== "scroll") {
    throw new Error(`${target.name} scoped table scroll is not enabled`);
  }
  return { overflowX };
}

function newCleanupReceipt() {
  return {
    profileRemoved: false,
    processExited: false,
    browserDisconnected: false,
    profileProcessesExited: false,
    processGroupsExited: false,
  };
}

async function disconnectBrowser() {
  if (!browser) {
    cleanup.browserDisconnected = true;
    return;
  }
  if (browser.isConnected()) {
    try {
      await browser.close();
    } catch (error) {
      if (!isUnexpectedBrowserClosure(error)) {
        throw error;
      }
    }
  }
  if (browser.isConnected()) {
    throw new Error("owned Chromium connection remains open");
  }
  cleanup.browserDisconnected = true;
}

function signalProcessGroup(pgid, signal) {
  if (!Number.isSafeInteger(pgid) || pgid <= 0) {
    throw new Error("refusing to signal an invalid process group");
  }
  try {
    process.kill(-pgid, signal);
  } catch (error) {
    if (error.code !== "ESRCH") {
      throw error;
    }
  }
}

async function recordProfileProcessTrees() {
  const { stdout } = await execFileAsync("ps", ["-eo", "pid=,args="]);
  for (const line of stdout.trim().split("\n")) {
    const match = line.trim().match(/^(\d+)\s+(.*)$/);
    if (match?.[2].includes(profile)) {
      await recordOwnedProcessTree(Number.parseInt(match[1], 10));
    }
  }
}

async function recordOwnedProcessTree(rootPID) {
  const { stdout } = await execFileAsync("ps", ["-eo", "pid=,ppid="]);
  const children = new Map();
  for (const line of stdout.trim().split("\n")) {
    const [pid, parentPID] = line.trim().split(/\s+/).map(Number);
    if (!Number.isSafeInteger(pid) || !Number.isSafeInteger(parentPID)) {
      continue;
    }
    const siblings = children.get(parentPID) ?? [];
    siblings.push(pid);
    children.set(parentPID, siblings);
  }
  const pending = [rootPID];
  const seen = new Set();
  while (pending.length > 0) {
    const pid = pending.pop();
    if (!Number.isSafeInteger(pid) || seen.has(pid)) {
      continue;
    }
    seen.add(pid);
    const identity = await processIdentity(pid);
    if (identity) {
      recordOwnedProcess(identity);
    }
    pending.push(...(children.get(pid) ?? []));
  }
}

async function processIdentity(pid) {
  if (!Number.isSafeInteger(pid) || pid <= 0) {
    return undefined;
  }
  try {
    const stat = await fs.readFile(`/proc/${pid}/stat`, "utf8");
    const fields = stat.slice(stat.lastIndexOf(")") + 2).trim().split(/\s+/);
    const group = Number(fields[2]);
    const startTime = fields[19];
    if (!Number.isSafeInteger(group) || group <= 0 || !startTime) {
      return undefined;
    }
    return { pid, group, startTime };
  } catch (error) {
    if (error.code === "ENOENT" || error.code === "ESRCH") {
      return undefined;
    }
    throw error;
  }
}

function recordOwnedProcess(identity) {
  const processes = ownedProcessGroups.get(identity.group) ?? new Map();
  processes.set(identity.pid, identity.startTime);
  ownedProcessGroups.set(identity.group, processes);
}

async function liveOwnedProcessGroups() {
  const liveGroups = [];
  for (const [group, processes] of ownedProcessGroups) {
    let live = false;
    for (const [pid, startTime] of processes) {
      const identity = await processIdentity(pid);
      if (!identity || identity.group !== group || identity.startTime !== startTime) {
        processes.delete(pid);
        continue;
      }
      live = true;
    }
    if (live) {
      liveGroups.push(group);
    } else {
      ownedProcessGroups.delete(group);
    }
  }
  return liveGroups;
}

async function signalProfileProcessGroups(signal) {
  for (const group of await liveOwnedProcessGroups()) {
    signalProcessGroup(group, signal);
  }
}

async function stopProfileProcesses() {
  await signalProfileProcessGroups("SIGTERM");
  if (await profileProcessesGone(1_500)) {
    return;
  }
  await signalProfileProcessGroups("SIGKILL");
  await waitForProfileProcesses(5_000);
}

async function profileProcessesGone(timeout) {
  return stableAbsence(
    async () => (await liveOwnedProcessGroups()).length > 0,
    timeout,
  );
}

async function waitForProfileProcesses(timeout) {
  if (!(await profileProcessesGone(timeout))) {
    throw new Error("owned Chromium profile process did not exit");
  }
}

async function waitForOwnedProcessGroupsExit() {
  if (!(await profileProcessesGone(5_000))) {
    throw new Error("owned Chromium process group did not exit");
  }
}

async function stableAbsence(isPresent, timeout) {
  const deadline = Date.now() + timeout;
  let absentChecks = 0;
  while (Date.now() < deadline) {
    if (await isPresent()) {
      absentChecks = 0;
    } else {
      absentChecks += 1;
      if (absentChecks === 10) {
        return true;
      }
    }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  return false;
}

async function profileExists(profilePath) {
  try {
    await fs.lstat(profilePath);
    return true;
  } catch (error) {
    if (error.code === "ENOENT") {
      return false;
    }
    throw error;
  }
}

async function removeProfile(profilePath) {
  const deadline = Date.now() + 5_000;
  while (true) {
    try {
      await fs.rm(profilePath, { recursive: true, force: true, maxRetries: 0 });
      if (!(await profileExists(profilePath))) {
        return;
      }
      throw new Error("owned Chromium profile directory remains");
    } catch (error) {
      if (Date.now() >= deadline) {
        throw error;
      }
      await new Promise((resolve) => setTimeout(resolve, 100));
    }
  }
}
