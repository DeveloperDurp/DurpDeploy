import { chromium } from "playwright";
import { mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";

import {
	addAuthenticator,
	check,
	createUser,
	mintToken,
	passwordLogin,
	startApp,
	stopApp,
} from "./mfa_browser_support.mjs";

const secretPattern = /password|token|secret|seed|recovery|challenge|assertion|credential|session|cookie|webauthn|passkey|bearer/gi;

function safeScenario(value) {
	const scenario = value.replace(/[^a-z0-9-]/gi, "-").replace(/-+/g, "-");
	check(scenario !== "", "failure artifact scenario is required");
	return scenario;
}

function safeURL(value) {
	try {
		const url = new URL(value);
		return `${url.origin}${url.pathname}`;
	} catch {
		return "unavailable";
	}
}

function redact(value, secrets) {
	let result = String(value);
	for (const secret of secrets) result = result.replaceAll(secret, "[REDACTED]");
	return result.replace(secretPattern, "[REDACTED]");
}

function observe(page, observability, secrets) {
	const requests = new Map();
	const record = (kind) => observability.errors.push({
		kind,
		message: "redacted browser error",
	});
	page.on("request", (request) => {
		const key = `${request.method()} ${safeURL(request.url())}`;
		requests.set(key, (requests.get(key) ?? 0) + 1);
	});
	page.on("console", (message) => {
		if (message.type() === "error") record("console");
	});
	page.on("pageerror", () => record("page"));
	return requests;
}

async function redactScreenshot(page) {
	await page.locator("input, textarea, [data-secret], .recovery-code, #totp-manual-key").evaluateAll(
		(elements) => {
			const sensitive = /password|token|secret|seed|recovery|challenge|assertion|credential|session|cookie|webauthn|passkey|bearer/i;
			for (const element of elements) {
				const identity = [element.id, element.className, element.getAttribute("name"), element.getAttribute("data-secret")].join(" ");
				if (element instanceof HTMLInputElement || element instanceof HTMLTextAreaElement) {
					if (element.type === "password" || sensitive.test(identity)) element.value = "[REDACTED]";
				} else if (element.matches("#totp-manual-key") || sensitive.test(identity)) {
					element.textContent = "[REDACTED]";
				}
			}
		},
	);
	await page.locator('img[alt="Authenticator setup QR code"]').evaluateAll(
		(elements) => {
			for (const element of elements) {
				const marker = document.createElement("div");
				marker.dataset.redactionMarker = "totp-enrollment-qr";
				marker.textContent = "[REDACTED TOTP QR]";
				element.replaceWith(marker);
			}
		},
	);
}

async function failureArtifact(page, fixture, scenario, error) {
	const directory = join(fixture.artifactRoot, safeScenario(scenario));
	await rm(directory, { force: true, recursive: true });
	await mkdir(directory, { recursive: true });
	await redactScreenshot(page);
	await page.screenshot({ path: join(directory, "screenshot.redacted.png") });
	const metadata = {
		scenario: safeScenario(scenario),
		error: redact(error instanceof Error ? error.message : "unexpected failure", fixture.secrets),
		url: safeURL(page.url()),
		requests: Object.fromEntries(fixture.requestCounts(page)),
		console_and_page_errors: fixture.observability.errors,
		cookies: await fixture.inspectCookies(page.context()),
		trace: "redacted browser metadata only; no Playwright network payload retained",
		cleanup: "pending",
	};
	await Promise.all([
		writeFile(join(directory, "trace.redacted.json"), `${JSON.stringify(metadata, null, 2)}\n`),
		writeFile(join(directory, "browser.redacted.log"), `${metadata.console_and_page_errors.map((entry) => `${entry.kind}: ${entry.message}`).join("\n")}\n`),
	]);
	fixture.artifacts.add(directory);
}

async function markArtifactsCleaned(artifacts) {
	for (const directory of artifacts) {
		const path = join(directory, "trace.redacted.json");
		const metadata = JSON.parse(await readFile(path, "utf8"));
		metadata.cleanup = "complete";
		await writeFile(path, `${JSON.stringify(metadata, null, 2)}\n`);
	}
}

export async function startBrowserFixture(root) {
	const app = await startApp(root);
	let browser;
	const contexts = new Set();
	const requestCounts = new WeakMap();
	const secrets = new Set();
	const fixture = {
		app,
		artifactRoot: join(root, "artifacts", "auth-mfa", "browser"),
		artifacts: new Set(),
		browser: null,
		observability: { errors: [] },
		requestCounts: (page) => requestCounts.get(page) ?? new Map(),
		secrets,
		async inspectCookies(context) {
			return (await context.cookies()).map((cookie) => ({
				expires: cookie.expires,
				httpOnly: cookie.httpOnly,
				name: cookie.name,
				path: cookie.path,
				sameSite: cookie.sameSite,
				secure: cookie.secure,
			}));
		},
		async newContext(options = {}) {
			const context = await browser.newContext({
				javaScriptEnabled: options.javaScriptEnabled ?? true,
				viewport: options.viewport ?? { height: 900, width: 1280 },
			});
			contexts.add(context);
			const page = await context.newPage();
			requestCounts.set(page, observe(page, fixture.observability, secrets));
			return { context, page };
		},
		async namedUsers(users) {
			for (const user of Object.values(users)) secrets.add(user.password);
			const admin = await fixture.newContext();
			await passwordLogin(admin.page, app.url, users.admin.email, users.admin.password);
			const token = await mintToken(admin.page, app.url);
			try {
				for (const role of ["deployer", "viewer"]) {
					await createUser(admin.page, app.url, token, users[role]);
				}
			} finally {
				secrets.add(token);
			}
			const deployer = await fixture.newContext();
			await passwordLogin(deployer.page, app.url, users.deployer.email, users.deployer.password);
			const viewer = await fixture.newContext();
			await passwordLogin(viewer.page, app.url, users.viewer.email, users.viewer.password);
			return { admin, deployer, viewer };
		},
		addAuthenticator,
		async run(scenario, page, action) {
			try {
				return await action();
			} catch (error) {
				await failureArtifact(page, fixture, scenario, error);
				throw error;
			}
		},
		async close() {
			const results = await Promise.allSettled([
				...Array.from(contexts, (context) => context.close()),
				browser?.close(),
				stopApp(app),
			]);
			await markArtifactsCleaned(fixture.artifacts);
			const failure = results.find((result) => result.status === "rejected");
			if (failure?.status === "rejected") throw failure.reason;
		},
	};
	try {
		browser = await chromium.launch({ executablePath: chromium.executablePath(), headless: true });
		fixture.browser = browser;
		return fixture;
	} catch (error) {
		await stopApp(app);
		throw error;
	}
}
