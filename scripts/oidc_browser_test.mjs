import { access, appendFile, mkdir, readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";

const sensitive = /password|token|secret|code|state|nonce|challenge|assertion|credential|session|cookie|bearer/i;
const viewports = [
	{ name: "desktop", width: 1280, height: 900 },
	{ name: "mobile", width: 375, height: 812 },
];
const passwordFallback = {
	email: "fixture@example.test",
	password: "browser-fallback-password",
};

function check(condition, message) {
  if (!condition) throw new Error(message);
}

function loopbackURL(raw, scheme) {
  const url = new URL(raw);
  check(url.protocol === `${scheme}:`, `readiness ${scheme} URL has the wrong scheme`);
  check(url.hostname === "127.0.0.1" || url.hostname === "::1", "readiness URL is not loopback");
  check(url.username === "" && url.password === "" && url.search === "" && url.hash === "", "readiness URL contains unsupported components");
  return url;
}

function parseReadiness(raw) {
  const value = JSON.parse(raw);
  check(value && typeof value === "object" && !Array.isArray(value), "readiness JSON must be an object");
  const keys = Object.keys(value).sort();
  check(keys.join(",") === "app_url,idp_url,pid", "readiness JSON must contain only app_url, idp_url, and pid");
  check(typeof value.app_url === "string" && typeof value.idp_url === "string", "readiness URLs are required");
  check(Number.isSafeInteger(value.pid) && value.pid > 0, "readiness PID is invalid");
  return { app: loopbackURL(value.app_url, "http"), idp: loopbackURL(value.idp_url, "https"), pid: value.pid };
}

function safeURL(raw) {
  try {
    const url = new URL(raw);
    return `${url.origin}${url.pathname}`;
  } catch {
    return "unavailable";
  }
}

function redactError(message) {
  return message.replace(/https?:\/\/[^\s"'`]+/g, safeURL);
}

function safeName(value) {
  const name = value.replace(/[^a-z0-9-]/gi, "-").replace(/-+/g, "-");
  check(name !== "", "scenario name is required");
  return name;
}

async function prepareEvidence(directory) {
  await mkdir(directory, { recursive: true, mode: 0o700 });
}

async function writeEvidence(directory, name, value) {
  await writeFile(join(directory, name), `${JSON.stringify(value, null, 2)}\n`, { mode: 0o600 });
}

async function appendNotepad(directory, line) {
  await appendFile(join(directory, "notepad.log"), `${line}\n`, { mode: 0o600 });
}

async function preflight(evidenceDir) {
  await prepareEvidence(evidenceDir);
  try {
    const { chromium } = await import("playwright");
    const executablePath = process.env.DURPDEPLOY_OIDC_BROWSER_EXECUTABLE ?? chromium.executablePath();
    await access(executablePath);
    await writeEvidence(evidenceDir, "preflight.json", { status: "ready" });
    await appendNotepad(evidenceDir, "Chromium preflight passed.");
    return { chromium, executablePath };
  } catch {
    await writeEvidence(evidenceDir, "preflight.json", {
      status: "blocked",
      reason: "Chromium executable unavailable",
    });
    await appendNotepad(evidenceDir, "Chromium preflight blocked; no browser scenario was executed.");
    throw new Error("Chromium executable unavailable; install or configure Playwright Chromium before running OIDC browser tests");
  }
}

function isAllowedURL(raw, readiness) {
  const url = new URL(raw);
  return url.origin === readiness.app.origin || url.origin === readiness.idp.origin;
}

async function redactPage(page) {
  await page.locator("input, textarea, [data-secret]").evaluateAll((elements) => {
    const sensitiveField = /password|token|secret|code|state|nonce|challenge|assertion|credential|session|cookie|bearer/i;
    for (const element of elements) {
      const identity = [element.id, element.getAttribute("name"), element.getAttribute("data-secret")].join(" ");
      if (element instanceof HTMLInputElement || element instanceof HTMLTextAreaElement) {
        if (element.type === "password" || sensitiveField.test(identity)) element.value = "[REDACTED]";
      } else if (sensitiveField.test(identity)) {
        element.textContent = "[REDACTED]";
      }
    }
  });
}

async function screenshot(page, evidenceDir, scenario) {
  await redactPage(page);
  await page.screenshot({ path: join(evidenceDir, `${safeName(scenario)}.redacted.png`), fullPage: true });
}

function observe(page, readiness) {
  const observations = {
    console_errors: 0,
    page_errors: 0,
    console: [],
    page_error_messages: [],
    requests: new Map(),
  };
  page.on("request", (request) => {
    const url = request.url();
    if (!isAllowedURL(url, readiness)) return;
    const key = `${request.method()} ${safeURL(url)}`;
    observations.requests.set(key, (observations.requests.get(key) ?? 0) + 1);
  });
  page.on("console", (message) => {
    if (message.type() === "error") observations.console_errors += 1;
    observations.console.push({ type: message.type(), message: redactError(message.text()) });
  });
  page.on("pageerror", (error) => {
    observations.page_errors += 1;
    observations.page_error_messages.push(redactError(error.message));
  });
  return observations;
}

function observationEvidence(observations) {
  const requests = [...observations.requests]
    .map(([request, count]) => {
      const [method, rawURL] = request.split(" ", 2);
      return { request: `${method} ${safeURL(rawURL)}`, count };
    })
    .sort((left, right) => left.request.localeCompare(right.request));
  return {
    console_errors: observations.console_errors,
    page_errors: observations.page_errors,
    console: observations.console.map(({ type, message }) => ({
      type,
      message: redactError(message),
    })),
    page_error_messages: observations.page_error_messages.map(redactError),
    requests,
  };
}

async function writeObservation(evidenceDir, scenario, observations) {
  await writeEvidence(
    evidenceDir,
    `${safeName(scenario)}.observations.redacted.json`,
    observationEvidence(observations),
  );
}

async function localTLSContext(browser, readiness, viewport) {
  const context = await browser.newContext({
    // The route below denies every origin except readiness-validated loopback app/IDP origins.
    ignoreHTTPSErrors: true,
    viewport,
  });
  await context.route("**/*", (route) => {
    try {
      return isAllowedURL(route.request().url(), readiness) ? route.continue() : route.abort("blockedbyclient");
    } catch {
      return route.abort("blockedbyclient");
    }
  });
  return context;
}

async function expectSSOLink(page) {
  const link = page.getByRole("link", { name: /single sign-on|sso|fixture sso/i });
  try {
    await link.waitFor({ timeout: 5_000 });
  } catch {
    throw new Error("tagged fixture did not expose an OIDC SSO link");
  }
  return link;
}

async function desktopMobileFocus(browser, readiness, evidenceDir) {
  for (const viewport of viewports) {
    const context = await localTLSContext(browser, readiness, viewport);
    try {
      const page = await context.newPage();
      const observations = observe(page, readiness);
      await page.goto(`${readiness.app.origin}/login`, { waitUntil: "networkidle" });
      const sso = await expectSSOLink(page);
      await sso.focus();
      check(await sso.evaluate((element) => document.activeElement === element), `${viewport.name} SSO link cannot receive focus`);
      check(!(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth)), `${viewport.name} login page overflows horizontally`);
      check(observations.console_errors === 0 && observations.page_errors === 0, `${viewport.name} login emitted a browser error`);
      await screenshot(page, evidenceDir, `login-${viewport.name}`);
      await writeObservation(evidenceDir, `login-${viewport.name}`, observations);
    } finally {
      await context.close();
    }
  }
}

async function loginSSO(browser, readiness, evidenceDir) {
  const context = await localTLSContext(browser, readiness, viewports[0]);
  try {
    const page = await context.newPage();
    const observations = observe(page, readiness);
    await page.goto(`${readiness.app.origin}/login`);
    await Promise.all([
      page.waitForURL((url) => url.origin === readiness.app.origin && url.pathname === "/"),
      (await expectSSOLink(page)).click(),
    ]);
    check(observations.console_errors === 0 && observations.page_errors === 0, "SSO login emitted a browser error");
    await screenshot(page, evidenceDir, "sso-login");
    await writeObservation(evidenceDir, "sso-login", observations);
    return context;
  } catch (error) {
    await context.close();
    throw error;
  }
}

async function logoutAndVerifyReauthLink(context, readiness, evidenceDir) {
  const page = context.pages()[0];
  const observations = observe(page, readiness);
  await page.goto(`${readiness.app.origin}/settings/security/reauth`);
  const sso = await expectSSOLink(page);
  await sso.focus();
  check(await sso.evaluate((element) => document.activeElement === element), "OIDC reauthentication link cannot receive focus");
  await screenshot(page, evidenceDir, "sso-reauth");
  await page.goto(`${readiness.app.origin}/`);
  await page.locator("details[data-account-menu] summary:visible").click();
  const logout = page.locator(
    'form[action="/logout"] button[type="submit"]:visible',
  );
  await Promise.all([
    page.waitForURL((url) => url.origin === readiness.app.origin && url.pathname === "/login"),
    logout.click(),
  ]);
  check(observations.console_errors === 0 && observations.page_errors === 0, "logout emitted a browser error");
  await screenshot(page, evidenceDir, "logout");
  await writeObservation(evidenceDir, "logout-reauth", observations);
}

async function providerOutage(browser, readiness, evidenceDir) {
  const context = await localTLSContext(browser, readiness, viewports[0]);
  try {
    const page = await context.newPage();
    const observations = observe(page, readiness);
    await page.goto(`${readiness.app.origin}/login`);
    const outage = page.waitForResponse((response) => {
      return response.url() === `${readiness.app.origin}/login/oidc` && response.status() === 503;
    });
    await (await expectSSOLink(page)).click();
    await outage;
    await page.getByText("Single sign-on is temporarily unavailable").waitFor();
    await expectSSOLink(page);
    const email = page.locator('input[name="email"]');
    const password = page.locator('input[name="password"]');
    await email.waitFor();
    await password.waitFor();
    await screenshot(page, evidenceDir, "provider-outage");
    await email.fill(passwordFallback.email);
    await password.fill(passwordFallback.password);
    await Promise.all([
      page.waitForURL((url) => url.origin === readiness.app.origin && url.pathname === "/"),
      page.locator('button[type="submit"]').click(),
    ]);
    await writeObservation(evidenceDir, "provider-outage", observations);
    check(observations.page_errors === 0, "OIDC outage fallback emitted a page error");
  } finally {
    await context.close();
  }
}

async function run(readinessFile, evidenceDir, outage) {
  const readiness = parseReadiness(await readFile(readinessFile, "utf8"));
  const { chromium, executablePath } = await preflight(evidenceDir);
  const browser = await chromium.launch({ executablePath, headless: true });
  try {
    if (outage) {
      await providerOutage(browser, readiness, evidenceDir);
      await writeEvidence(evidenceDir, "browser.redacted.json", {
        status: "pass",
        scenarios: ["provider-outage"],
      });
    } else {
      await desktopMobileFocus(browser, readiness, evidenceDir);
      const context = await loginSSO(browser, readiness, evidenceDir);
      try {
        await logoutAndVerifyReauthLink(context, readiness, evidenceDir);
      } finally {
        await context.close();
      }
      await writeEvidence(evidenceDir, "browser.redacted.json", {
        status: "pass",
        scenarios: ["desktop-mobile-focus", "login-sso", "reauth-link", "logout"],
      });
    }
  } finally {
    await browser.close();
  }
}

async function selfTest() {
  const readiness = parseReadiness('{"app_url":"http://127.0.0.1:3000","idp_url":"https://127.0.0.1:4000","pid":1}');
  const outage = argumentsFor([
    "node",
    "oidc_browser_test.mjs",
    "--outage",
    "--ready-file",
    "ready.json",
    "--evidence-dir",
    "evidence",
  ]);
  const callbackFailure = redactError(
    "callback http://127.0.0.1:3000/login/oidc/callback?code=fixture-code&state=fixture-state",
  );
  check(readiness.app.hostname === "127.0.0.1", "self-test did not parse loopback app URL");
  check(outage.outage, "self-test did not parse the outage option");
  check(!callbackFailure.includes("fixture-code") && !callbackFailure.includes("fixture-state"), "self-test did not remove callback query data from errors");
  check(safeURL("https://127.0.0.1:4000/callback?code=secret&state=secret") === "https://127.0.0.1:4000/callback", "self-test did not remove callback query data");
  const observation = observationEvidence({
    console_errors: 0,
    page_errors: 0,
    console: [{ type: "error", message: "callback http://127.0.0.1:3000/callback?code=secret" }],
    page_error_messages: [],
    requests: new Map([["GET http://127.0.0.1:3000/login", 1]]),
  });
  check(observation.console[0].message === "callback http://127.0.0.1:3000/callback", "self-test did not redact console callback data");
  check(JSON.stringify(observation).includes("?") === false, "self-test did not remove observation query data");
  check(sensitive.test("token"), "self-test lost sensitive artifact classification");
  console.log("OIDC browser harness self-test: OK (no browser required)");
}

function argumentsFor(argv) {
  const values = {
    evidenceDir: "",
    outage: false,
    preflight: false,
    readinessFile: "",
    selfTest: false,
    validateReadyFile: "",
  };
  for (let index = 2; index < argv.length; index += 1) {
    const argument = argv[index];
    if (argument === "--outage") values.outage = true;
    else if (argument === "--preflight") values.preflight = true;
    else if (argument === "--self-test") values.selfTest = true;
    else if (argument === "--evidence-dir") values.evidenceDir = argv[++index] ?? "";
    else if (argument === "--ready-file") values.readinessFile = argv[++index] ?? "";
    else if (argument === "--validate-ready-file") values.validateReadyFile = argv[++index] ?? "";
    else throw new Error("usage: oidc_browser_test.mjs --self-test | --validate-ready-file FILE | --preflight --evidence-dir DIR | [--outage] --ready-file FILE --evidence-dir DIR");
  }
  return values;
}

async function main() {
  const options = argumentsFor(process.argv);
  if (options.selfTest) return selfTest();
  if (options.validateReadyFile) {
    parseReadiness(await readFile(options.validateReadyFile, "utf8"));
    return;
  }
  check(options.evidenceDir !== "", "--evidence-dir is required");
  if (options.preflight) return preflight(options.evidenceDir);
  check(options.readinessFile !== "", "--ready-file is required");
  return run(options.readinessFile, options.evidenceDir, options.outage);
}

main().catch((error) => {
  console.error(`OIDC browser harness failed: ${error instanceof Error ? redactError(error.message) : "unexpected failure"}`);
  process.exitCode = 1;
});
