import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { randomBytes } from "node:crypto";
import { spawn } from "node:child_process";
import { createServer } from "node:net";
import { createInterface } from "node:readline";

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
	const reservation = createServer();
	let server;
	try {
		await new Promise((resolve, reject) => {
			reservation.once("error", reject);
			reservation.listen(0, "127.0.0.1", resolve);
		});
		const port = reservation.address().port;
		const url = `http://localhost:${port}`;
		const env = {
			...process.env,
			DURPDEPLOY_ADDR: `127.0.0.1:${port}`,
			DURPDEPLOY_AGENT_LISTEN_ADDR: "127.0.0.1:0",
			DURPDEPLOY_AGENT_PUBLIC_URL: "https://localhost",
			DURPDEPLOY_AGENT_IDENTITY_DIR: join(dir, "agent-identity"),
			DURPDEPLOY_CONTAINER_NAMESPACE: `mfa_${randomBytes(16).toString("hex")}`,
			DURPDEPLOY_DB: db,
			DURPDEPLOY_SECRET_KEY: randomBytes(32).toString("base64"),
			DURPDEPLOY_URL: url,
			TMPDIR: dir,
		};
		await run("go", ["build", "-o", binary, "./cmd/server"], { cwd: root, env });
		await run(binary, ["admin", "create", "--email", "admin@mfa.test", "--password", "admin-password-1234"], { env });
		await new Promise((resolve, reject) => reservation.close((error) => error ? reject(error) : resolve()));
		server = spawn(binary, [], { env, stdio: ["ignore", "pipe", "ignore"] });
		let ready = false;
		let startupError;
		server.once("error", (error) => { startupError = error; });
		const logs = createInterface({ input: server.stdout });
		logs.on("line", (line) => {
			if (!line.startsWith("{")) return;
			const record = JSON.parse(line);
			if (record.msg === "server starting" && record.addr === env.DURPDEPLOY_ADDR) ready = true;
		});
		for (let attempt = 0; attempt < 100; attempt += 1) {
			if (startupError) throw startupError;
			if (server.exitCode !== null || server.signalCode !== null) break;
			if (ready) {
				const response = await fetch(`${url}/healthz`, { signal: AbortSignal.timeout(2000) });
				if (response.ok) return { dir, server, url };
			}
			await new Promise((resolve) => setTimeout(resolve, 100));
		}
		throw new Error("isolated MFA server did not become ready");
	} catch (error) {
		if (server) await stopApp({ dir, server });
		else await rm(dir, { force: true, recursive: true });
		throw error;
	} finally {
		if (reservation.listening) await new Promise((resolve) => reservation.close(resolve));
	}
}

export async function stopApp(app) {
	if (app.server.pid && app.server.exitCode === null && app.server.signalCode === null) {
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
		// The 303 lands on /settings/tokens?flash=<id>; the plaintext
		// lives only in that page's one-time banner (issue #32).
		const body = await response.text();
		return body.match(/ddp_pat_[0-9a-f]{64}/)?.[0] ?? "";
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
