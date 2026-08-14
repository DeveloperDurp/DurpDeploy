import { access, readFile, rm } from "node:fs/promises";
import { join } from "node:path";

import { check } from "./mfa_browser_support.mjs";
import { startBrowserFixture } from "./mfa_browser_fixture.mjs";

const root = new URL("..", import.meta.url).pathname;
const scenario = "fixture-forced-selector-failure";
const artifact = join(root, "artifacts", "auth-mfa", "browser", scenario);
let enrollmentSeed = "";
let screenshotSanitized = false;
const users = {
	admin: { email: "admin@mfa.test", password: "admin-password-1234" },
	deployer: { email: "deployer@mfa.test", name: "Deployer", password: "deployer-password-1234", role: "deployer" },
	viewer: { email: "viewer@mfa.test", name: "Viewer", password: "viewer-password-1234", role: "viewer" },
};

async function exists(path) {
	try {
		await access(path);
		return true;
	} catch {
		return false;
	}
}

async function run() {
	await rm(artifact, { force: true, recursive: true });
	const fixture = await startBrowserFixture(root);
	try {
		const roles = await fixture.namedUsers(users);
		await fixture.addAuthenticator(roles.deployer.context, roles.deployer.page, { isUserVerified: false });
		check(fixture.requestCounts(roles.admin.page).size > 0, "browser request counter is empty");
		check((await fixture.inspectCookies(roles.admin.context)).length > 0, "browser session inspection is empty");
		const noJS = await fixture.newContext({ javaScriptEnabled: false, viewport: { height: 600, width: 375 } });
		await noJS.page.goto(`${fixture.app.url}/login`);
		check(await noJS.page.locator('input[name="email"]').isVisible(), "JavaScript-disabled context did not render login");
		check(fixture.observability.errors.length === 0, "browser emitted console or page errors");
		if (process.argv.includes("--force-selector-failure")) {
			await roles.admin.page.goto(`${fixture.app.url}/settings/security/reauth`);
			await roles.admin.page.locator('input[name="password"]').fill(users.admin.password);
			await roles.admin.page.getByRole("button", { name: "Continue" }).click();
			await roles.admin.page.getByRole("button", { name: "Set up authenticator" }).click();
			await roles.admin.page.getByRole("heading", { name: "Set up authenticator" }).waitFor();
			enrollmentSeed = (await roles.admin.page.locator("#totp-manual-key").textContent())?.trim() ?? "";
			check(enrollmentSeed, "TOTP enrollment did not show a manual key");
			fixture.secrets.add(enrollmentSeed);
			const qr = roles.admin.page.locator('img[alt="Authenticator setup QR code"]');
			check((await qr.getAttribute("src"))?.startsWith("data:image/png;base64,"), "TOTP enrollment QR source is missing");
			const screenshot = roles.admin.page.screenshot.bind(roles.admin.page);
			roles.admin.page.screenshot = async (options) => {
				check(await roles.admin.page.locator("#totp-manual-key").textContent() === "[REDACTED]", "TOTP enrollment manual key was not redacted before screenshot capture");
				check(await qr.count() === 0, "TOTP enrollment QR was not removed before screenshot capture");
				check(await roles.admin.page.locator('[data-redaction-marker="totp-enrollment-qr"]').count() === 1, "TOTP enrollment QR redaction marker is missing before screenshot capture");
				screenshotSanitized = true;
				return screenshot(options);
			};
			try {
				await fixture.run(scenario, roles.admin.page, async () => {
					throw new Error("forced enrollment failure");
				});
			} catch (error) {
				check(screenshotSanitized, "failure screenshot was not captured after TOTP redaction");
				throw error;
			}
		}
		check(!(await exists(artifact)), "success created failure artifacts");
		console.log("MFA browser fixture smoke: OK (no secret output)");
	} finally {
		await fixture.close();
	}
}

run().catch(async (error) => {
	if (process.argv.includes("--force-selector-failure")) {
		const metadata = await readFile(join(artifact, "trace.redacted.json"), "utf8");
		const log = await readFile(join(artifact, "browser.redacted.log"), "utf8");
		check(await exists(join(artifact, "screenshot.redacted.png")), "failure screenshot was not created");
		check(metadata.includes('"cleanup": "complete"'), "failure artifact did not record cleanup");
		for (const secret of Object.values(users).map((user) => user.password)) {
			check(!metadata.includes(secret), "failure artifact contains a test secret");
			check(!log.includes(secret), "failure log contains a test secret");
		}
		check(!metadata.includes(enrollmentSeed), "failure artifact metadata contains a TOTP enrollment seed");
		check(!log.includes(enrollmentSeed), "failure log contains a TOTP enrollment seed");
		check(screenshotSanitized, "failure screenshot capture did not verify TOTP redaction");
	}
	console.error(`MFA browser fixture failed: ${error instanceof Error ? error.message : "unexpected failure"}`);
	process.exitCode = 1;
});
