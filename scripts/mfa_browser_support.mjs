import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { randomBytes } from "node:crypto";
import { spawn } from "node:child_process";

export function check(condition, message) {
	if (!condition) throw new Error(message);
}

function run(command, args, options = {}) {
	return new Promise((resolve, reject) => {
		const child = spawn(command, args, { stdio: "ignore", ...options });
		child.once("error", reject);
		child.once("exit", (code) => {
			if (code === 0) resolve();
			else reject(new Error(`${command} exited with ${code}`));
		});
	});
}

export async function startApp(root) {
	const dir = await mkdtemp(join(tmpdir(), "durpdeploy-mfa-browser-"));
	const binary = join(dir, "durpdeploy");
	const db = join(dir, "durpdeploy.db");
	const url = "http://localhost:8081";
	const env = {
		...process.env,
		DURPDEPLOY_ADDR: "127.0.0.1:8081",
		DURPDEPLOY_DB: db,
		DURPDEPLOY_SECRET_KEY: randomBytes(32).toString("base64"),
		DURPDEPLOY_URL: url,
	};
	await run("go", ["build", "-o", binary, "./cmd/server"], { cwd: root, env });
	await run(binary, ["admin", "create", "--email", "admin@mfa.test", "--password", "admin-password-1234"], { env });
	const server = spawn(binary, [], { env, stdio: "ignore" });
	for (let attempt = 0; attempt < 50; attempt += 1) {
		try {
			const response = await fetch(`${url}/healthz`);
			if (response.ok) return { dir, server, url };
		} catch {
			await new Promise((resolve) => setTimeout(resolve, 100));
		}
	}
	server.kill();
	await rm(dir, { force: true, recursive: true });
	throw new Error("isolated MFA server did not become ready");
}

export async function stopApp(app) {
	if (app.server.exitCode === null && app.server.signalCode === null) {
		const exited = new Promise((resolve) => app.server.once("exit", resolve));
		app.server.kill("SIGTERM");
		await exited;
	}
	await rm(app.dir, { force: true, recursive: true });
}

export async function addAuthenticator(context, page, options = {}) {
	const cdp = await context.newCDPSession(page);
	await cdp.send("WebAuthn.enable");
	const { authenticatorId } = await cdp.send("WebAuthn.addVirtualAuthenticator", {
		options: {
			automaticPresenceSimulation: true,
			hasResidentKey: true,
			hasUserVerification: true,
			isUserVerified: true,
			protocol: "ctap2",
			transport: "internal",
			...options,
		},
	});
	return { authenticatorId, cdp };
}

export async function passwordLogin(page, url, email, password) {
	await page.goto(`${url}/login`);
	await page.locator('input[name="email"]').fill(email);
	await page.locator('input[name="password"]').fill(password);
	await Promise.all([
		page.waitForURL((current) => current.pathname === "/" || current.pathname === "/login/mfa"),
		page.locator('button[type="submit"]').click(),
	]);
}

export async function csrf(page) {
	return page.locator('meta[name="csrf-token"]').getAttribute("content");
}

export async function mintToken(page, url) {
	const token = await page.evaluate(async () => {
		const csrfToken = document.querySelector('meta[name="csrf-token"]')?.content;
		const response = await fetch("/settings/tokens", {
			body: new URLSearchParams({ csrf_token: csrfToken ?? "", name: "mfa-browser" }),
			credentials: "same-origin",
			method: "POST",
		});
		if (!response.ok) return "";
		return new URL(response.url).searchParams.get("new_token") ?? "";
	});
	check(token !== "", "could not create isolated bearer token");
	return token;
}

export async function createUser(page, url, token, user) {
	const response = await fetch(`${url}/api/v1/admin/users`, {
		body: JSON.stringify(user),
		headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
		method: "POST",
	});
	check(response.ok, "could not create isolated browser user");
	return response.json();
}
