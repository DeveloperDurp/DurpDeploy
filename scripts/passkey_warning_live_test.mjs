import { chromium } from "playwright";
import { readFile, writeFile, mkdir } from "node:fs/promises";
import { createHash, randomBytes, X509Certificate } from "node:crypto";
import { execFileSync } from "node:child_process";
import { join } from "node:path";
import { addAuthenticator, check, mintToken, passwordLogin } from "./mfa_browser_support.mjs";

// Use a dedicated stack. Retain its account and virtual key for manual repeats.
const record = JSON.parse(await readFile(process.argv[2], "utf8"));
check(record.status === "ready", "the dedicated stack must be ready");
const artifactDir = process.argv[3];
check(artifactDir, "provide an artifact directory");
await mkdir(artifactDir, { recursive: true });
const identityPath = join(record.state_dir, "passkey-warning-identity.json");
let identity;
try {
	identity = JSON.parse(await readFile(identityPath, "utf8"));
} catch (error) {
	if (error.code !== "ENOENT") throw error;
	identity = { email: "counter-review@example.test", password: randomBytes(24).toString("base64url") };
	const key = (await readFile(join(record.state_dir, "secret-key"), "utf8")).trim();
	execFileSync(record.runtime, ["exec", "--user=10001", "-e", "DURPDEPLOY_SECRET_KEY",
		`${record.stack_id}-app`, "/usr/local/bin/durpdeploy", "admin", "create",
		"--email", identity.email, "--password", identity.password], {
		env: { ...process.env, DURPDEPLOY_SECRET_KEY: key }, stdio: "pipe",
	});
	await writeFile(identityPath, JSON.stringify(identity), { mode: 0o600 });
}
const certificate = new X509Certificate(await readFile(join(record.state_dir, "tls/cert.pem")));
const spki = createHash("sha256").update(certificate.publicKey.export({ type: "spki", format: "der" })).digest("base64");
const host = new URL(record.url).hostname;
const browser = await chromium.launch({ args: [
	`--ignore-certificate-errors-spki-list=${spki}`,
	`--host-resolver-rules=MAP ${host} 127.0.0.1`, "--no-proxy-server",
] });
let authenticator;
try {
	const context = await browser.newContext();
	const page = await context.newPage();
	authenticator = await addAuthenticator(context, page);
	const params = { authenticatorId: authenticator.authenticatorId };
	if (identity.credential) {
		await authenticator.cdp.send("WebAuthn.addCredential", { ...params, credential: identity.credential });
	}
	await passwordLogin(page, record.url, identity.email, identity.password);
	if (new URL(page.url()).pathname === "/login/mfa") {
		await page.getByRole("button", { name: "Use a passkey" }).click();
		await page.waitForURL(`${record.url}/`);
	}
	const token = await mintToken(page, record.url);
	if (!identity.credential) {
		await page.goto(`${record.url}/settings/security`);
		await page.locator("#passkey-name").fill("Retained counter review key");
		await page.getByRole("button", { name: "Add passkey" }).click();
		await page.getByRole("button", { name: "Verify passkey" }).click();
		await page.getByRole("button", { name: "Continue", exact: true }).click();
		await page.waitForURL(`${record.url}/login`);
		await passwordLogin(page, record.url, identity.email, identity.password);
		await page.getByRole("button", { name: "Use a passkey" }).click();
		await page.waitForURL(`${record.url}/`);
	}
	const { credentials } = await authenticator.cdp.send("WebAuthn.getCredentials", params);
	check(credentials.length === 1 && credentials[0].signCount > 0, "missing counter credential");
	await authenticator.cdp.send("WebAuthn.removeCredential", { ...params, credentialId: credentials[0].credentialId });
	await authenticator.cdp.send("WebAuthn.addCredential", {
		...params, credential: { ...credentials[0], signCount: credentials[0].signCount - 1 },
	});
	await passwordLogin(page, record.url, identity.email, identity.password);
	await page.getByRole("button", { name: "Use a passkey" }).click();
	await page.waitForURL(`${record.url}/`);
	const metadata = await page.evaluate(async (token) => {
		const get = async (path) => {
			const response = await fetch(path, { headers: { Authorization: `Bearer ${token}` } });
			if (!response.ok) throw new Error(`GET ${path}: ${response.status}`);
			return response.json();
		};
		return { passkeys: await get("/api/v1/security/passkeys"), audit: await get("/api/v1/admin/audit?action=mfa_passkey_clone_warning") };
	}, token);
	check(metadata.passkeys[0].clone_warning === true && metadata.audit.items.length > 0, "warning or event missing");
	check(JSON.parse(metadata.audit.items[0].details.String).purpose === "login", "wrong warning purpose");
	await writeFile(join(artifactDir, "api.json"), JSON.stringify(metadata, null, 2));
	for (const width of [390, 1280]) {
		await page.setViewportSize({ width, height: 1000 });
		await page.goto(`${record.url}/settings/security`);
		await page.locator("[data-passkey-counter-warning]").waitFor({ state: "visible" });
		await page.screenshot({ path: join(artifactDir, `passkey-warning-${width}.png`), fullPage: true });
	}
	await page.goto(`${record.url}/admin/audit?action=mfa_passkey_clone_warning`);
	await page.getByText("mfa_passkey_clone_warning", { exact: true }).first().waitFor();
	await page.getByText("View details", { exact: true }).first().click();
	await page.screenshot({ path: join(artifactDir, "passkey-audit.png"), fullPage: true });
	const current = await authenticator.cdp.send("WebAuthn.getCredentials", params);
	identity.credential = current.credentials[0];
	await writeFile(identityPath, JSON.stringify(identity), { mode: 0o600 });
	console.log(`PASS: retained equal-counter login, owner API, audit API, warning badge and audit page at ${record.url}`);
} finally {
	try {
		if (authenticator) {
			const { credentials } = await authenticator.cdp.send("WebAuthn.getCredentials", {
				authenticatorId: authenticator.authenticatorId,
			});
			if (credentials.length === 1) {
				identity.credential = credentials[0];
				await writeFile(identityPath, JSON.stringify(identity), { mode: 0o600 });
			}
		}
	} finally {
		await browser.close();
	}
}
