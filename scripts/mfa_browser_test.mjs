import { createHmac } from "node:crypto";
import { chromium } from "playwright";
import { mkdir, writeFile } from "node:fs/promises";
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

const root = new URL("..", import.meta.url).pathname;
const passwords = {
	admin: "admin-password-1234",
	deployer: "deployer-password-1234",
	viewer: "viewer-password-1234",
};
const longDeployerName = "Deployer With An Intentionally Long Display Name For Navbar Regression Coverage";

function emitScenario(id) {
	console.log(JSON.stringify({ scenario_id: id, engine: "sqlite", result: "pass" }));
}

async function visibleAccountSummary(page, role) {
	const summary = page.locator("details[data-account-menu] > summary:visible").filter({ hasText: `(${role})` });
	await summary.waitFor();
	return summary;
}

async function revealMobileAccountMenu(page) {
	const mobileNav = page.locator("details[data-mobile-nav]");
	if (
		(await mobileNav.isVisible()) &&
		!(await mobileNav.evaluate((element) => element.open))
	) {
		await mobileNav.locator(":scope > summary").click();
		await mobileNav.locator(":scope > ul").waitFor({ state: "visible" });
	}
}

function monitorBrowserErrors(page, errors) {
	page.on("console", (message) => {
		if (message.type() === "error") errors.push(message.text());
	});
	page.on("pageerror", (error) => errors.push(error.message));
}

async function accountContract(page, url, role, tokensVisible, artifactDir) {
	for (const width of [375, 768, 1280]) {
		await page.setViewportSize({ height: 900, width });
		await page.goto(`${url}/`);
		await revealMobileAccountMenu(page);
		const summary = await visibleAccountSummary(page, role);
		const summaryElement = await summary.elementHandle();
		check(summaryElement !== null, `${role} account summary is missing`);
		if (role === "deployer") {
			const label = `${longDeployerName} (${role})`;
			check(
				await summary.evaluate((element, expected) =>
					element.title === expected && element.getAttribute("aria-label") === expected,
					label,
				),
				`long account name is not discoverable at ${width}px`,
			);
		}
		await summary.focus();
		await page.keyboard.press("Enter");
		const menu = summary.locator("xpath=following-sibling::ul");
		if (width === 375) {
			check(
				await summary.evaluate((element) =>
					element.scrollHeight <= element.clientHeight,
				),
				`${role} account summary wraps at ${width}px`,
			);
		}
		await check(await menu.locator('a[href="/settings/security"]').isVisible(), `${role} lacks Security`);
		await check((await menu.locator('a[href="/settings/tokens"]').count()) === (tokensVisible ? 1 : 0), `${role} token visibility is wrong`);
		await check(await menu.locator('button[type="submit"]', { hasText: "Logout" }).isVisible(), `${role} lacks Logout`);
		const labels = await menu.locator("a, button").allTextContents();
		check(labels.join("|").startsWith(tokensVisible ? "Security|Tokens|Logout" : "Security|Logout"), `${role} account order is wrong`);
		await page.keyboard.press("Escape");
		await page.waitForFunction((element) => !element.parentElement?.open, summaryElement);
		await page.waitForFunction((element) => document.activeElement === element, summaryElement);
		await check(await summaryElement.evaluate((element) => document.activeElement === element), `${role} escape did not return focus`);
		await revealMobileAccountMenu(page);
		await summary.focus();
		await page.keyboard.press("Space");
		await page.locator("body").click({ position: { x: 1, y: 1 } });
		await page.waitForFunction((element) => !element.parentElement?.open, summaryElement);
		await page.waitForFunction((element) => document.activeElement === element, summaryElement);
		await check(!(await summaryElement.evaluate((element) => element.parentElement?.open)), `${role} outside click did not close menu`);
		await check(await summaryElement.evaluate((element) => document.activeElement === element), `${role} outside click did not return focus`);
		const mobileNav = page.locator("details[data-mobile-nav]");
		if (await mobileNav.isVisible()) {
			const mobileSummary = mobileNav.locator(":scope > summary");
			const mobileSummaryElement = await mobileSummary.elementHandle();
			check(mobileSummaryElement !== null, "mobile navigation summary is missing");
			await mobileSummary.focus();
			await page.keyboard.press("Escape");
			await page.waitForFunction((element) => !element.parentElement?.open, mobileSummaryElement);
			await page.waitForFunction((element) => document.activeElement === element, mobileSummaryElement);
			await check(await mobileSummaryElement.evaluate((element) => document.activeElement === element), "mobile navigation escape did not return focus");
			await mobileSummary.focus();
			await page.keyboard.press("Space");
			await page.locator("body").click({ position: { x: 1, y: 1 } });
			await page.waitForFunction((element) => !element.parentElement?.open, mobileSummaryElement);
			await page.waitForFunction((element) => document.activeElement === element, mobileSummaryElement);
			await check(await mobileSummaryElement.evaluate((element) => document.activeElement === element), "mobile navigation outside click did not return focus");
		}
		const clipped = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth);
		check(!clipped, `${role} clips at ${width}px`);
		await page.screenshot({ path: join(artifactDir, `${role}-${width}.png`) });
	}
	await page.goto(`${url}/settings/security`);
	const active = await (await visibleAccountSummary(page, role)).getAttribute("class");
	check(active?.includes("active"), `${role} security path is not active`);
	const topLevelTokens = await page.locator('a[href="/settings/tokens"]').evaluateAll((links) => links.some((link) => !link.closest("details[data-account-menu]")));
	check(!topLevelTokens, "Tokens escaped the account dropdown");
}

async function reauthenticate(page, url) {
	await page.goto(`${url}/settings/security/reauth`);
	await page.locator('input[name="password"]').fill(passwords.deployer);
	await page.getByRole("button", { name: "Continue" }).click();
	const reauthPasskey = page.getByRole("button", { name: "Use a passkey" });
	if (await reauthPasskey.isVisible()) {
		await reauthPasskey.click();
	}
	await page.getByRole("heading", { name: "Security" }).waitFor();
}

function totpCode(seed, at = Date.now()) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
	let buffer = 0;
	let bits = 0;
	const bytes = [];
	for (const character of seed) {
		if (character === "=") break;
		buffer = (buffer << 5) | alphabet.indexOf(character);
		bits += 5;
		if (bits >= 8) {
			bytes.push((buffer >>> (bits - 8)) & 0xff);
			bits -= 8;
		}
	}
	const counter = Buffer.alloc(8);
	counter.writeBigUInt64BE(BigInt(Math.floor(at / 30_000)));
	const digest = createHmac("sha1", Buffer.from(bytes)).update(counter).digest();
	const offset = digest[digest.length - 1] & 0x0f;
	const value = ((digest[offset] & 0x7f) << 24) |
		(digest[offset + 1] << 16) |
		(digest[offset + 2] << 8) |
		digest[offset + 3];
	return String(value % 1_000_000).padStart(6, "0");
}

async function enrollTOTP(page, url) {
	await reauthenticate(page, url);
	await page.getByRole("button", { name: "Set up authenticator" }).click();
	await page.getByRole("heading", { name: "Set up authenticator" }).waitFor();
	const seed = (await page.locator("#totp-manual-key").textContent())?.trim();
	check(seed, "TOTP enrollment did not show a manual key");
	await page.locator("#totp-confirm-code").fill(totpCode(seed));
	await Promise.all([
		page.waitForURL(`${url}/login`),
		page.getByRole("button", { name: "Confirm authenticator" }).click(),
	]);
	await passkeyLogin(page, url);
	return seed;
}

async function passkeyDeleteConfirmationContract(page, url, token, artifactDir) {
	await reauthenticate(page, url);
	await page.goto(`${url}/settings/security`);
	const form = page.locator('form[data-passkey-delete-dialog]').first();
	const opener = form.getByRole("button", { name: "Delete" });
	const dialogID = await form.getAttribute("data-passkey-delete-dialog");
	check(dialogID !== null, "passkey delete form is missing its dialog target");
	const dialog = page.locator(`#${dialogID}`);
	let posts = 0;
	const countDeletePost = (request) => {
		if (
			request.method() === "POST" &&
			new URL(request.url()).pathname === "/settings/security/passkeys/delete"
		) {
			posts += 1;
		}
	};
	page.on("request", countDeletePost);
	try {
		for (const [width, dismiss] of [
			[375, async () => dialog.getByRole("button", { name: "Cancel", exact: true }).press("Escape")],
			[768, async () => dialog.getByRole("button", { name: "Cancel", exact: true }).click()],
			[1280, async () => dialog.click({ position: { x: 1, y: 1 } })],
		]) {
			await page.setViewportSize({ height: 900, width });
			await opener.click();
			await dialog.waitFor({ state: "visible" });
			await page.screenshot({ path: join(artifactDir, `passkey-delete-dialog-${width}.png`) });
			await dialog.getByRole("button", { name: "Cancel", exact: true }).focus();
			await dismiss();
			check(!(await dialog.evaluate((element) => element.open)), "passkey delete dismissal did not close the dialog");
			await check(
				await opener.evaluate((element) => document.activeElement === element),
				"passkey delete dismissal did not restore opener focus",
			);
			check(!(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth)), `passkey delete dialog clips at ${width}px`);
		}
		check(posts === 0, "passkey delete dismissal submitted a request");
		await opener.click();
		await dialog.waitFor({ state: "visible" });
		await Promise.all([
			page.waitForResponse((response) =>
				response.status() === 303 &&
				new URL(response.url()).pathname === "/settings/security/passkeys/delete",
			),
			page.waitForURL(`${url}/settings/security`),
			dialog.getByRole("button", { name: "Confirm delete" }).click(),
		]);
		check(posts === 1, "passkey delete confirm did not submit exactly once");
		await page.goto(`${url}/`);
		check(new URL(page.url()).pathname === "/", "passkey deletion ended the current browser session");
		const tokenResponse = await fetch(`${url}/api/v1/users/me`, {
			headers: { Authorization: `Bearer ${token}` },
		});
		check(tokenResponse.ok, "passkey deletion revoked bearer API token");
	} finally {
		page.off("request", countDeletePost);
	}
}

async function securityDisableConfirmationContract(page, url, token, artifactDir) {
	await page.goto(`${url}/settings/security`);
	const form = page.locator('form[data-security-disable-dialog]');
	const opener = form.getByRole("button", { name: "Disable MFA" });
	const dialogID = await form.getAttribute("data-security-disable-dialog");
	check(dialogID !== null, "security disable form is missing its dialog target");
	const dialog = page.locator(`#${dialogID}`);
	let posts = 0;
	const countDisablePost = (request) => {
		if (
			request.method() === "POST" &&
			new URL(request.url()).pathname === "/settings/security/disable"
		) {
			posts += 1;
		}
	};
	page.on("request", countDisablePost);
	try {
		for (const [width, dismiss] of [
			[375, async () => dialog.getByRole("button", { name: "Cancel", exact: true }).press("Escape")],
			[768, async () => dialog.getByRole("button", { name: "Cancel", exact: true }).click()],
			[1280, async () => dialog.click({ position: { x: 1, y: 1 }})],
		]) {
			await page.setViewportSize({ height: 900, width });
			await opener.click();
			await dialog.waitFor({ state: "visible" });
			await page.screenshot({ path: join(artifactDir, `security-disable-dialog-${width}.png`) });
			await dialog.getByRole("button", { name: "Cancel", exact: true }).focus();
			await dismiss();
			check(!(await dialog.evaluate((element) => element.open)), "security disable dismissal did not close the dialog");
			await check(
				await opener.evaluate((element) => document.activeElement === element),
				"security disable dismissal did not restore opener focus",
			);
			check(!(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth)), `security disable dialog clips at ${width}px`);
		}
		check(posts === 0, "security disable dismissal submitted a request");
		await opener.click();
		await dialog.waitFor({ state: "visible" });
		await Promise.all([
			page.waitForResponse((response) =>
				response.status() === 303 &&
				new URL(response.url()).pathname === "/settings/security/disable",
			),
			page.waitForURL(`${url}/login`),
			dialog.getByRole("button", { name: "Confirm disable" }).click(),
		]);
		check(posts === 1, "security disable confirm did not submit exactly once");
		const tokenResponse = await fetch(`${url}/api/v1/users/me`, {
			headers: { Authorization: `Bearer ${token}` },
		});
		check(tokenResponse.ok, "security disable revoked bearer API token");
	} finally {
		page.off("request", countDisablePost);
	}
}

async function enrollPasskey(page, url, name, authenticator, artifactDir) {
	await reauthenticate(page, url);
	if (authenticator) {
		// Chrome allows one internal virtual authenticator; remove its first
		// credential after reauthentication to model enrolling a second device.
		const { credentials } = await authenticator.cdp.send(
			"WebAuthn.getCredentials",
			{ authenticatorId: authenticator.authenticatorId },
		);
		check(credentials.length === 1, "virtual authenticator credential count is wrong");
		await authenticator.cdp.send("WebAuthn.removeCredential", {
			authenticatorId: authenticator.authenticatorId,
			credentialId: credentials[0].credentialId,
		});
	}
	await page.locator("#passkey-name").fill(name);
	await page.getByRole("button", { name: "Add passkey" }).click();
	await page.getByRole("heading", { name: "Test your passkey" }).waitFor();
	for (const width of [375, 768, 1280]) {
		await page.setViewportSize({ height: 900, width });
		await page.screenshot({ path: join(artifactDir, `passkey-test-${width}.png`) });
	}
	await page.getByRole("button", { name: "Verify passkey" }).click();
	const recoveryCodes = page.locator(".recovery-code");
	await Promise.race([
		recoveryCodes.first().waitFor({ state: "visible" }),
		page.waitForURL(`${url}/login`),
	]);
	if (await recoveryCodes.count()) {
		check(await recoveryCodes.count() === 10, "first passkey verification did not reveal ten recovery codes");
		await Promise.all([
			page.waitForURL(`${url}/login`),
			page.getByRole("button", { name: "Continue" }).click(),
		]);
	}
	await passwordLogin(page, url, "deployer@mfa.test", passwords.deployer);
	await page.getByRole("button", { name: "Use a passkey" }).click();
	await page.waitForURL(`${url}/`);
}

async function passkeyLogin(page, url) {
	await passwordLogin(page, url, "deployer@mfa.test", passwords.deployer);
	check(new URL(page.url()).pathname === "/login/mfa", "enrolled password reached a full session");
	await page.getByRole("button", { name: "Use a passkey" }).click();
	await page.waitForURL(`${url}/`);
}

async function configuredPasskeyContract(page, url) {
	await reauthenticate(page, url);
	for (const width of [375, 768, 1280]) {
		await page.setViewportSize({ height: 900, width });
		await page.goto(`${url}/settings/security`);
		await check(
			await page.getByRole("heading", { name: "Authenticator app" }).isVisible(),
			`authenticator enrollment card is missing at ${width}px`,
		);
		await check(
			await page.getByRole("heading", { name: "Add passkey" }).isVisible(),
			`passkey enrollment card is missing at ${width}px`,
		);
		await check(
			!(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth)),
			`security page clips at ${width}px`,
		);
	}
	await check(
		await page.getByRole("heading", { name: "Authenticator app" }).isVisible(),
		"authenticator enrollment card is missing",
	);
	await check(
		await page.getByRole("heading", { name: "Add passkey" }).isVisible(),
		"passkey enrollment card is missing",
	);
	await check(
		!(await page.getByRole("button", { name: "Set up authenticator" }).isDisabled()),
		"unconfigured authenticator enrollment is disabled",
	);
	await check(
		await page.getByRole("button", { name: "Add passkey" }).isDisabled(),
		"configured passkey enrollment is enabled",
	);
	await check(
		await page
			.locator('form[action="/settings/security/passkeys/delete"]')
			.getByRole("button", { name: "Delete" })
			.isDisabled(),
		"last passkey deletion is enabled",
	);
	await check(
		await page.getByText(
			"Keep another factor configured before deleting this passkey.",
		).isVisible(),
		"last passkey protection is missing",
	);
}

async function fallbackContract(browser, url, kind, errors) {
	const context = await browser.newContext();
	if (kind === "cancel") {
		await context.addInitScript(() => { navigator.credentials.get = async () => { throw new DOMException("cancelled", "AbortError"); }; });
	} else {
		await context.addInitScript(() => { Object.defineProperty(window, "PublicKeyCredential", { value: undefined }); });
	}
	const page = await context.newPage();
	monitorBrowserErrors(page, errors);
	await passwordLogin(page, url, "deployer@mfa.test", passwords.deployer);
	await page.getByRole("button", { name: "Use a passkey" }).click();
	await page.locator("[data-webauthn-status]").waitFor();
	check(new URL(page.url()).pathname === "/login/mfa", `${kind} fallback issued a session`);
	await context.close();
}

async function run() {
	const app = await startApp(root);
	const artifactDir = join(root, ".omo", "evidence", "task-14-browser");
	let browser;
	try {
		await mkdir(artifactDir, { recursive: true });
		browser = await chromium.launch({
			executablePath: chromium.executablePath(),
			headless: true,
		});
			const adminContext = await browser.newContext({ viewport: { height: 900, width: 1280 } });
			const adminPage = await adminContext.newPage();
		const browserErrors = [];
		monitorBrowserErrors(adminPage, browserErrors);
		await passwordLogin(adminPage, app.url, "admin@mfa.test", passwords.admin);
		const adminToken = await mintToken(adminPage, app.url);
		const deployer = await createUser(adminPage, app.url, adminToken, { email: "deployer@mfa.test", name: longDeployerName, password: passwords.deployer, role: "deployer" });
		const viewer = await createUser(adminPage, app.url, adminToken, { email: "viewer@mfa.test", name: "Viewer", password: passwords.viewer, role: "viewer" });
		await accountContract(adminPage, app.url, "admin", true, artifactDir);

		const deployerContext = await browser.newContext({ viewport: { height: 900, width: 1280 } });
		const deployerPage = await deployerContext.newPage();
		monitorBrowserErrors(deployerPage, browserErrors);
		await passwordLogin(deployerPage, app.url, "deployer@mfa.test", passwords.deployer);
		const deployerToken = await mintToken(deployerPage, app.url);
		await accountContract(deployerPage, app.url, "deployer", true, artifactDir);
		await addAuthenticator(deployerContext, deployerPage);
		await enrollPasskey(
			deployerPage,
			app.url,
			"virtual authenticator one",
			null,
			artifactDir,
		);
		await fallbackContract(browser, app.url, "cancel", browserErrors);
		await fallbackContract(browser, app.url, "unsupported", browserErrors);
		await passkeyLogin(deployerPage, app.url);
		emitScenario("webauthn-login-ceremony");
		await deployerPage.goto("http://127.0.0.1:8081/login");
		await deployerPage.locator('input[name="email"]').fill("deployer@mfa.test");
		await deployerPage.locator('input[name="password"]').fill(passwords.deployer);
		await deployerPage.getByRole("button", { name: "Login" }).click();
		await deployerPage.getByRole("button", { name: "Use a passkey" }).click();
		await deployerPage.locator("[data-webauthn-status]").waitFor();
		check(new URL(deployerPage.url()).pathname === "/login/mfa", "wrong origin accepted a passkey");
		await passkeyLogin(deployerPage, app.url);
		await configuredPasskeyContract(deployerPage, app.url);
		const deployerTOTPSeed = await enrollTOTP(deployerPage, app.url);
		await passkeyDeleteConfirmationContract(
			deployerPage,
			app.url,
			deployerToken,
			artifactDir,
		);
		await passwordLogin(
			deployerPage,
			app.url,
			"deployer@mfa.test",
			passwords.deployer,
		);
		await deployerPage.locator("#totp-code").fill(
			totpCode(deployerTOTPSeed, Date.now() + 30_000),
		);
		await Promise.all([
			deployerPage.waitForURL(`${app.url}/`),
			deployerPage.getByRole("button", { name: "Continue" }).click(),
		]);
		await securityDisableConfirmationContract(
			deployerPage,
			app.url,
			deployerToken,
			artifactDir,
		);
		emitScenario("passkey-factor-lifecycle");

		const viewerContext = await browser.newContext({ viewport: { height: 900, width: 1280 } });
		const viewerPage = await viewerContext.newPage();
		monitorBrowserErrors(viewerPage, browserErrors);
		await passwordLogin(viewerPage, app.url, "viewer@mfa.test", passwords.viewer);
		await accountContract(viewerPage, app.url, "viewer", false, artifactDir);
		await viewerPage.goto(`${app.url}/settings/security/reauth`);
		await viewerPage.locator('input[name="password"]').fill(passwords.viewer);
		await viewerPage.getByRole("button", { name: "Continue" }).click();
		await viewerPage.getByRole("heading", { name: "Security" }).waitFor();
		await viewerPage.goto(`${app.url}/`);
		const viewerSummary = await visibleAccountSummary(viewerPage, "viewer");
		await viewerSummary.click();
			const viewerLogout = viewerPage.waitForResponse(
				(response) => new URL(response.url()).pathname === "/logout",
			);
			await viewerSummary.locator("xpath=following-sibling::ul").getByRole("button", { name: "Logout" }).click();
			check((await viewerLogout).status() === 403, "viewer logout was not blocked");
			check(new URL(viewerPage.url()).pathname === "/logout", "viewer logout left the read-only error page");
			await viewerPage.evaluate(() => new Promise(requestAnimationFrame));
			const viewerLogoutError = browserErrors.findIndex((error) =>
				error.includes("403 (Forbidden)"),
			);
			check(viewerLogoutError !== -1, "viewer logout did not report its expected 403");
			browserErrors.splice(viewerLogoutError, 1);

			await adminPage.goto(`${app.url}/settings/security/reauth`);
		await adminPage.locator('input[name="password"]').fill(passwords.admin);
		await adminPage.getByRole("button", { name: "Continue" }).click();
		await adminPage.goto(`${app.url}/admin/users`);
		const deployerResetForm = adminPage.locator(
			`form[action="/admin/users/${deployer.id}/mfa-reset"]`,
		);
		const deployerResetOpener = deployerResetForm.getByRole("button", {
			name: "Reset MFA",
		});
		const deployerResetDialog = adminPage.locator(
			`#mfa-reset-dialog-${deployer.id}`,
		);
		const viewerResetOpener = adminPage
			.locator(`form[action="/admin/users/${viewer.id}/mfa-reset"]`)
			.getByRole("button", { name: "Reset MFA" });
		const viewerResetDialog = adminPage.locator(
			`#mfa-reset-dialog-${viewer.id}`,
		);
		let deployerResetPosts = 0;
		let viewerResetPosts = 0;
		adminPage.on("request", (request) => {
			if (
				request.method() === "POST" &&
				new URL(request.url()).pathname ===
					`/admin/users/${deployer.id}/mfa-reset`
			) {
				deployerResetPosts += 1;
			}
			if (
				request.method() === "POST" &&
				new URL(request.url()).pathname ===
					`/admin/users/${viewer.id}/mfa-reset`
			) {
				viewerResetPosts += 1;
			}
			});
			await viewerResetOpener.click();
			await viewerResetDialog.waitFor({ state: "visible" });
			await viewerResetDialog
				.getByRole("button", { name: "Cancel", exact: true })
				.focus();
			await viewerResetDialog
				.getByRole("button", { name: "Cancel", exact: true })
				.press("Escape");
			check(
				!(await viewerResetDialog.evaluate((dialog) => dialog.open)),
				"viewer MFA reset Escape did not close the dialog",
			);
		await check(
			await viewerResetOpener.evaluate(
				(element) => document.activeElement === element,
			),
			"viewer MFA reset Escape did not restore opener focus",
		);
		check(viewerResetPosts === 0, "viewer MFA reset Escape submitted a request");
		for (const dismiss of [
			async () =>
				deployerResetDialog
					.getByRole("button", { name: "Cancel", exact: true })
					.press("Escape"),
			async () =>
				deployerResetDialog
					.getByRole("button", { name: "Cancel", exact: true })
					.click(),
			async () =>
				deployerResetDialog.click({ position: { x: 1, y: 1 } }),
		]) {
			await deployerResetOpener.click();
			await deployerResetDialog.waitFor({ state: "visible" });
				await deployerResetDialog
					.getByRole("button", { name: "Cancel", exact: true })
					.focus();
				await dismiss();
				check(
					!(await deployerResetDialog.evaluate((dialog) => dialog.open)),
					"MFA reset dismissal did not close the dialog",
				);
			await check(
				await deployerResetOpener.evaluate(
					(element) => document.activeElement === element,
				),
				"MFA reset dismissal did not restore opener focus",
			);
		}
		check(deployerResetPosts === 0, "MFA reset dismissal submitted a request");
		await deployerResetOpener.click();
		await deployerResetDialog.waitFor({ state: "visible" });
		await Promise.all([
			adminPage.waitForResponse(
				(response) =>
					response.status() === 303 &&
					new URL(response.url()).pathname ===
						`/admin/users/${deployer.id}/mfa-reset`,
			),
			deployerResetDialog
				.getByRole("button", { name: "Confirm reset" })
				.click(),
		]);
		check(deployerResetPosts === 1, "MFA reset confirm did not submit exactly once");
		await deployerPage.goto(`${app.url}/`);
		check(new URL(deployerPage.url()).pathname === "/login", "admin reset did not end target browser session");
		const tokenResponse = await fetch(`${app.url}/api/v1/users/me`, { headers: { Authorization: `Bearer ${deployerToken}` } });
		check(tokenResponse.ok, "admin reset revoked bearer API token");
		const noJSContext = await browser.newContext({ javaScriptEnabled: false });
		await noJSContext.addCookies(await adminContext.cookies());
		const noJSPage = await noJSContext.newPage();
		monitorBrowserErrors(noJSPage, browserErrors);
			await noJSPage.goto(`${app.url}/admin/users`);
			const viewerResetForm = noJSPage.locator(
				`form[data-mfa-reset-dialog][action="/admin/users/${viewer.id}/mfa-reset"]`,
			);
			await check(
				(await viewerResetForm.locator('input[name="csrf_token"]').inputValue()) !== "",
				"no-JS MFA reset is missing its CSRF token",
			);
		await check(
			(await viewerResetForm.locator('input[name="reason"]').inputValue()) ===
				"administrative_reset",
			"no-JS MFA reset has the wrong reason",
		);
			const noJSResetResponse = noJSPage.waitForResponse(
				(response) =>
					new URL(response.url()).pathname ===
						`/admin/users/${viewer.id}/mfa-reset`,
			);
			const noJSResetNavigation = noJSPage.waitForNavigation({
				waitUntil: "domcontentloaded",
			});
			await viewerResetForm
				.getByRole("button", { name: "Reset MFA" })
				.evaluate((button) => button.click());
			await noJSResetNavigation;
			check(
				(await noJSResetResponse).status() === 303,
				"no-JS MFA reset did not redirect",
			);
		await noJSContext.close();
		await viewerPage.goto(`${app.url}/`);
		check(new URL(viewerPage.url()).pathname === "/login", "no-JS reset did not end target browser session");
		check(browserErrors.length === 0, `browser console errors: ${browserErrors.join(" | ")}`);
		await writeFile(join(artifactDir, "trace.redacted.json"), JSON.stringify({
			artifacts: "screenshots contain settings/navigation only; no trace network payload is retained",
			checks: ["CDP virtual authenticator with UV", "passkey lifecycle", "role navigation", "admin reset bearer survival"],
			redacted: true,
		}, null, 2));
		await viewerContext.close();
		await deployerContext.close();
		await adminContext.close();
		console.log("MFA browser journey: OK (no credential, cookie, challenge, assertion, or token output)");
	} finally {
		await browser?.close();
		await stopApp(app);
	}
}

run().catch((error) => {
	console.error(`MFA browser journey failed: ${error instanceof Error ? error.message : "unexpected failure"}`);
	process.exitCode = 1;
});
