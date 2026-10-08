import { randomBytes } from "node:crypto";
import { chromium } from "playwright";
import { addAuthenticator, check, createUser, mintToken, passwordLogin } from "./mfa_browser_support.mjs";

const url = process.env.DURPDEPLOY_BASE_URL;
check(url, "an owned E2E URL is required");
const user = {
	email: `origin-${randomBytes(12).toString("hex")}@e2e.test`,
	name: "E2E origin check",
	password: randomBytes(24).toString("hex"),
	role: "deployer",
};
const browser = await chromium.launch({ headless: true });
try {
	const context = await browser.newContext();
	const page = await context.newPage();
	page.setDefaultTimeout(10000);
	await passwordLogin(page, url,
		process.env.DURPDEPLOY_ADMIN_EMAIL || "e2e-admin@test.local",
		process.env.DURPDEPLOY_ADMIN_PASSWORD || "e2e-admin-password-1234");
	await createUser(page, url, await mintToken(page, url), user);
	await context.clearCookies();
	await addAuthenticator(context, page);
	await passwordLogin(page, url, user.email, user.password);
	await page.goto(`${url}/settings/security/reauth`);
	await page.locator('input[name="password"]').fill(user.password);
	await page.getByRole("button", { name: "Continue" }).click();
	await page.getByRole("heading", { name: "Security", exact: true }).waitFor();
	await page.locator("#passkey-name").fill("Allocated origin");
	const registration = page.waitForResponse((response) =>
		new URL(response.url()).pathname === "/settings/security/passkeys/finish");
	await page.getByRole("button", { name: "Add passkey" }).click();
	check((await registration).status() === 303, "allocated origin rejected passkey registration");
	await page.getByRole("heading", { name: "Test your passkey" }).waitFor();
	const verification = page.waitForResponse((response) =>
		new URL(response.url()).pathname === "/settings/security/passkeys/test/finish");
	await page.getByRole("button", { name: "Verify passkey" }).click();
	check((await verification).ok(), "allocated origin rejected passkey assertion");
	await page.locator(".recovery-code").first().waitFor();
	check(await page.locator(".recovery-code").count() === 10, "passkey setup did not complete");
	console.log(`Allocated E2E WebAuthn origin: PASS (${url})`);
} finally {
	await browser.close();
}
