import { randomBytes } from "node:crypto";
import { spawn } from "node:child_process";
import { chmod, mkdir, mkdtemp, readlink, rm, writeFile } from "node:fs/promises";
import net from "node:net";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";

import { FaultProxy } from "./agent_fault_proxy.mjs";
import { isLifecycleScenario, runLifecycleFault } from "./agent_lifecycle_faults.mjs";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const defaultAgentRoot = resolve(root, "../durpdeploy-agent-continue-remote-agent-rollout");
const evidenceFlag = process.argv.indexOf("--evidence-dir");
const faultFlag = process.argv.indexOf("--fault-scenario");
const lifecycle = process.argv.includes("--lifecycle");
const faultScenario = faultFlag >= 0 ? process.argv[faultFlag + 1] : "";
const evidenceDir = resolve(
	evidenceFlag >= 0 ? process.argv[evidenceFlag + 1] :
		join(root, ".omo/evidence/continue-remote-agent-rollout/browser"),
);
const agentRoot = resolve(process.env.DURPDEPLOY_AGENT_WORKTREE || defaultAgentRoot);
const runDir = await mkdtemp(join(tmpdir(), "durpdeploy-agent-browser-"));
const agentContainer = `durpdeploy-agent-e2e-${process.pid}`;
const agentStateVolume = `${agentContainer}-state`;
const serverBinary = join(runDir, "durpdeploy");
const agentBinary = join(runDir, "durpdeploy-agent");
const database = join(runDir, "durpdeploy.db");
const admin = { email: "admin@agent-proof.test", password: randomBytes(24).toString("hex") };
const viewer = { email: "viewer@agent-proof.test", password: randomBytes(24).toString("hex") };
const consoleErrors = [];
const layout = [];
const network = [];
const cleanup = { agentStopped: false, browserStopped: false, serverStopped: false, temporaryDirectoryRemoved: false };
let agent;
let agentErrors = "";
let agentRun = 0;
let browser;
let currentAgentContainer = agentContainer;
let lifecycleProxy;
let pairingProxy;
let server;
let serverErrors = "";

function check(condition, message) {
	if (!condition) throw new Error(message);
}

function command(commandName, args, options = {}) {
	return new Promise((resolvePromise, reject) => {
		const child = spawn(commandName, args, { stdio: ["ignore", "pipe", "pipe"], ...options });
		let stdout = "";
		let stderr = "";
		child.stdout.on("data", (chunk) => { stdout += chunk; });
		child.stderr.on("data", (chunk) => { stderr += chunk; });
		child.once("error", reject);
		child.once("exit", (code) => {
			if (code === 0) resolvePromise(stdout);
			else reject(new Error(`${commandName} exited ${code}: ${redact(stderr)}`));
		});
	});
}

function sqliteWriteLock(database) {
	const child = spawn("sqlite3", [database], { stdio: ["pipe", "pipe", "pipe"] });
	let output = "";
	let errors = "";
	child.stdout.on("data", (chunk) => { output += chunk; });
	child.stderr.on("data", (chunk) => { errors += chunk; });
	child.stdin.write(".bail on\nPRAGMA busy_timeout=5000;\nBEGIN IMMEDIATE;\nSELECT 'LOCKED';\n");
	return waitFor(() => output.includes("LOCKED") ? true :
		(child.exitCode === null ? false : Promise.reject(
			new Error(`SQLite lock failed: ${errors}`))), "SQLite write lock").then(() => ({
		release: async () => {
			const exited = new Promise((resolvePromise) => child.once("exit", resolvePromise));
			child.stdin.end("ROLLBACK;\n");
			await exited;
		},
		trace: ["BEGIN IMMEDIATE", "SELECT 'LOCKED'", "ROLLBACK"],
	}));
}

function redact(value) {
	return value
		.replace(/ddp_pat_[\w-]+/g, "<redacted>")
		.replace(/(password|secret|token|claim|code)[=:]\S+/gi, "$1=<redacted>")
		.replace(/-----BEGIN [^-]+-----[\s\S]*?-----END [^-]+-----/g, "<redacted-pem>");
}

async function stop(child, field) {
	if (child?.exitCode === null) {
		const exited = new Promise((resolvePromise) => child.once("exit", resolvePromise));
		child.kill("SIGTERM");
		const stopped = await Promise.race([
			exited.then(() => true),
			new Promise((resolvePromise) => setTimeout(() => resolvePromise(false), 5000)),
		]);
		if (!stopped) {
			child.kill("SIGKILL");
			await exited;
		}
	}
	cleanup[field] = !child || child.exitCode !== null;
}

async function reserveAddress() {
	const socket = net.createServer();
	await new Promise((resolvePromise, reject) => {
		socket.once("error", reject);
		socket.listen(0, "127.0.0.1", resolvePromise);
	});
	const address = socket.address();
	check(address && typeof address !== "string", "failed to reserve dynamic address");
	return { address: `127.0.0.1:${address.port}`, socket };
}

async function waitForHealth(baseURL) {
	for (let attempt = 0; attempt < 150; attempt += 1) {
		if (server && server.exitCode !== null) {
			throw new Error(`server exited before health check: ${server.exitCode}: ${redact(serverErrors)}`);
		}
		try {
			if ((await fetch(`${baseURL}/healthz`, { signal: AbortSignal.timeout(1000) })).ok) return;
		} catch (error) {
			if (!(error instanceof Error)) throw error;
		}
		await new Promise((resolvePromise) => setTimeout(resolvePromise, 100));
	}
	throw new Error("server did not become healthy");
}

async function waitFor(checkValue, description, attempts = 300) {
	for (let attempt = 0; attempt < attempts; attempt += 1) {
		const value = await checkValue();
		if (value) return value;
		await new Promise((resolvePromise) => setTimeout(resolvePromise, 20));
	}
	throw new Error(`timed out waiting for ${description}`);
}

function pairingOffer(child) {
	return new Promise((resolvePromise, reject) => {
		let output = "";
		const timer = setTimeout(() => reject(new Error("agent did not print a pairing offer")), 15000);
		child.stdout.on("data", (chunk) => {
			output += chunk;
			const code = output.match(/^Pairing code: (.+)$/m)?.[1];
			const fingerprint = output.match(/^Agent fingerprint: (.+)$/m)?.[1];
			if (code && fingerprint) {
				clearTimeout(timer);
				resolvePromise({ code, fingerprint });
			}
		});
		child.once("error", reject);
		child.once("exit", (code) => reject(new Error(`agent exited before pairing: ${code}`)));
	});
}

async function login(page, baseURL, credentials) {
	await page.goto(`${baseURL}/login`);
	await page.getByLabel("Email").fill(credentials.email);
	await page.getByLabel("Password").fill(credentials.password);
	await Promise.all([
		page.waitForURL(`${baseURL}/`),
		page.getByRole("button", { name: "Login" }).click(),
	]);
}

async function logout(page, baseURL) {
	await Promise.all([
		page.waitForURL(`${baseURL}/login`),
		page.locator('form[action="/logout"]').first().evaluate((form) => form.requestSubmit()),
	]);
}

async function capture(page, name, width) {
	await page.setViewportSize({ width, height: 900 });
	await page.screenshot({ path: join(evidenceDir, `${name}-${width}.png`), fullPage: true });
	await writeFile(join(evidenceDir, `${name}-${width}.accessibility.yml`), `${await page.locator("body").ariaSnapshot()}\n`);
	const dimensions = await page.evaluate(() => ({
		offenders: [...document.querySelectorAll("body *")]
			.map((element) => {
				const rect = element.getBoundingClientRect();
				return { className: element.className, right: rect.right, tagName: element.tagName, text: element.textContent?.trim().slice(0, 80) };
			})
			.filter((element) => element.right > innerWidth)
			.sort((left, right) => right.right - left.right)
			.slice(0, 100),
		overflow: document.documentElement.scrollWidth > innerWidth,
		scrollWidth: document.documentElement.scrollWidth,
	}));
	layout.push({ name, width, ...dimensions });
}

async function main() {
	await rm(evidenceDir, { recursive: true, force: true });
	await mkdir(evidenceDir, { recursive: true });
	const browserReservation = await reserveAddress();
	const listenerReservation = await reserveAddress();
	const bootstrapReservation = await reserveAddress();
	lifecycleProxy = await FaultProxy.start({
		listenHost: "0.0.0.0",
		upstreamAddress: listenerReservation.address,
	});
	pairingProxy = await FaultProxy.start({ upstreamAddress: bootstrapReservation.address });
	const baseURL = `http://${browserReservation.address}`;
	const listenerPort = lifecycleProxy.address.slice(lifecycleProxy.address.lastIndexOf(":") + 1);
	const lifecycleAddress = `127.0.0.1:${listenerPort}`;
	const listenerURL = `https://host.containers.internal:${listenerPort}`;
	const bootstrapURL = `https://${pairingProxy.address}`;
	const nonce = randomBytes(12).toString("hex");
	const sentinelDir = join(runDir, "server-path");
	const sentinelMarker = join(runDir, "server-bash-invoked");
	await mkdir(sentinelDir, { mode: 0o700 });
	await writeFile(join(sentinelDir, "bash"), `#!/bin/sh\n: > '${sentinelMarker}'\nexit 97\n`, { mode: 0o700 });
	const serverEnvironment = {
		...process.env,
		LANG: "ddp-server-execution-sentinel",
		PATH: `${sentinelDir}:${process.env.PATH}`,
		DURPDEPLOY_ADDR: browserReservation.address,
		DURPDEPLOY_AGENT_IDENTITY_DIR: join(runDir, "server-identity"),
		DURPDEPLOY_AGENT_LISTEN_ADDR: listenerReservation.address,
		DURPDEPLOY_AGENT_PUBLIC_URL: listenerURL,
		DURPDEPLOY_DB: `${database}?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)`,
		DURPDEPLOY_EXECUTION_BOUNDARY: "service",
		DURPDEPLOY_SECRET_KEY: randomBytes(32).toString("base64"),
	};
	const serverIdentity = serverEnvironment.DURPDEPLOY_AGENT_IDENTITY_DIR;
	const startServer = async () => {
		server = spawn(serverBinary, [], {
			cwd: runDir,
			env: serverEnvironment,
			stdio: ["ignore", "pipe", "pipe"],
		});
		server.stdout.on("data", (chunk) => { serverErrors += chunk; });
		server.stderr.on("data", (chunk) => { serverErrors += chunk; });
		cleanup.serverStopped = false;
		await waitForHealth(baseURL);
	};
	const restartServer = async () => {
		await stop(server, "serverStopped");
		await startServer();
	};
	const readOnly = (query) => command("sqlite3", ["-readonly", database, query]);
	const agentEnvironment = {
		...process.env,
		DURPDEPLOY_AGENT_E2E_BINARY: agentBinary,
		DURPDEPLOY_EXTRA_SCRUB_PATTERNS: "todo12-secret",
		DURPDEPLOY_AGENT_LISTEN_ADDR: `0.0.0.0:${bootstrapReservation.address.slice(bootstrapReservation.address.lastIndexOf(":") + 1)}`,
		DURPDEPLOY_AGENT_STATE_DIR: "/var/lib/durpdeploy-agent",
		DURPDEPLOY_AGENT_VERSION: "todo12-browser-proof",
		LANG: `ddp-agent-${nonce}`,
	};
	const startAgent = () => {
		agentRun += 1;
		currentAgentContainer = `${agentContainer}-${agentRun}`;
		agent = spawn("bash", [join(root, "scripts/run_agent_e2e_container.sh")], {
			cwd: runDir,
			env: {
				...agentEnvironment,
				DURPDEPLOY_AGENT_E2E_CONTAINER: currentAgentContainer,
				DURPDEPLOY_AGENT_E2E_STATE_VOLUME: agentStateVolume,
			},
			stdio: ["ignore", "pipe", "pipe"],
		});
		agent.stderr.on("data", (chunk) => { agentErrors += chunk; });
		cleanup.agentStopped = false;
		return agent;
	};
	const stopAgent = async () => {
		await command("podman", ["rm", "-f", "--ignore", currentAgentContainer]);
		await stop(agent, "agentStopped");
	};
	const restartAgent = async () => {
		await stopAgent();
		startAgent();
	};
	await mkdir(serverIdentity, { recursive: true, mode: 0o700 });
	await command("openssl", ["genpkey", "-algorithm", "ED25519", "-out", join(serverIdentity, "identity.key")]);
	await command("openssl", [
		"req", "-new", "-x509", "-key", join(serverIdentity, "identity.key"),
		"-out", join(serverIdentity, "identity.crt"), "-days", "1",
		"-subj", "/CN=host.containers.internal", "-addext",
		"subjectAltName=DNS:host.containers.internal,IP:127.0.0.1",
	]);
	await command("go", ["build", "-buildvcs=false", "-o", serverBinary, "./cmd/server"], { cwd: root, env: serverEnvironment });
	await command("go", ["build", "-buildvcs=false", "-o", agentBinary, "./cmd/agent"], {
		cwd: agentRoot,
		env: { ...process.env, CGO_ENABLED: "0" },
	});
	await chmod(agentBinary, 0o755);
	await command(serverBinary, ["admin", "create", "--email", admin.email, "--password", admin.password], { cwd: runDir, env: serverEnvironment });
	await Promise.all([
		new Promise((resolvePromise) => browserReservation.socket.close(resolvePromise)),
		new Promise((resolvePromise) => listenerReservation.socket.close(resolvePromise)),
		new Promise((resolvePromise) => bootstrapReservation.socket.close(resolvePromise)),
	]);
	await startServer();
	startAgent();
	const offer = await pairingOffer(agent);
	browser = await chromium.launch({ headless: true });
	const context = await browser.newContext();
	const page = await context.newPage();
	page.on("console", (message) => { if (message.type() === "error") consoleErrors.push(message.text()); });
	page.on("pageerror", (error) => consoleErrors.push(error.message));
	page.on("response", (response) => {
		const url = new URL(response.url());
		if (url.origin === baseURL && !url.pathname.startsWith("/static/")) {
			network.push({ method: response.request().method(), path: url.pathname, status: response.status() });
		}
	});
	await login(page, baseURL, admin);
	await page.goto(`${baseURL}/admin/agents`);
	for (const width of [375, 768, 1280]) await capture(page, "agents-before-pair", width);
	await page.getByLabel("HTTPS address").fill(bootstrapURL);
	await page.getByLabel("Pairing code").fill(offer.code);
	await page.getByLabel("Certificate fingerprint").fill(offer.fingerprint);
	const pairingScenarios = new Set([
		"crash-before-first-pair-request",
		"lost-first-phase-response",
		"crash-before-server-activation",
		"crash-before-cleanup-ack",
		"lost-cleanup-ack-response",
	]);
	const submitPairing = () => Promise.all([
		page.waitForURL(/\/admin\/agents\/[^/]+$/),
		page.getByRole("button", { name: "Pair agent" }).click(),
	]);
	const refillPairing = async () => {
		await page.goto(`${baseURL}/admin/agents`);
		await page.getByLabel("HTTPS address").fill(bootstrapURL);
		await page.getByLabel("Pairing code").fill(offer.code);
		await page.getByLabel("Certificate fingerprint").fill(offer.fingerprint);
	};
	let pairingCheckpoint = null;
	if (pairingScenarios.has(faultScenario)) {
		const rule = faultScenario === "crash-before-first-pair-request" ||
			faultScenario === "crash-before-server-activation" ||
			faultScenario === "crash-before-cleanup-ack" ? {
			direction: "client-to-upstream", action: "gate",
			skip: faultScenario === "crash-before-cleanup-ack" ? 3 : 2,
		} : {
			direction: "upstream-to-client", action: "gate",
			skip: faultScenario === "lost-first-phase-response" ? 1 : 2,
		};
		pairingProxy.arm(rule);
		const firstAttempt = submitPairing().catch((error) => error);
		const gate = await pairingProxy.nextGate();
		const state = await waitFor(async () => {
			const value = (await readOnly(
				"SELECT a.status||'|'||p.state FROM agents a " +
				"JOIN agent_pairings p ON p.agent_id=a.id LIMIT 1;",
			)).trim();
			if (faultScenario === "crash-before-first-pair-request" ||
				faultScenario === "lost-first-phase-response" ||
				faultScenario === "crash-before-server-activation") {
				return value === "pending|committing" ? value : "";
			}
			return value === "active|paired" ? value : "";
		}, "durable pairing checkpoint");
		pairingCheckpoint = { gate, state };
		if (faultScenario === "crash-before-server-activation") {
			const lock = await sqliteWriteLock(database);
			const responseSequence = pairingProxy.transcript.length;
			gate.forward();
			await waitFor(() => pairingProxy.transcript.slice(responseSequence).some(
				(event) => event.direction === "upstream-to-client" &&
					event.action === "forward" && event.bytes >= 100,
			), "forwarded first-phase response");
			const blockedState = (await readOnly(
				"SELECT a.status||'|'||p.state FROM agents a " +
				"JOIN agent_pairings p ON p.agent_id=a.id LIMIT 1;",
			)).trim();
			check(blockedState === "pending|committing",
				`activation escaped lock: ${blockedState}`);
			await stop(server, "serverStopped");
			await lock.release();
			pairingCheckpoint.barrier = lock.trace;
			await startServer();
		} else {
			if (faultScenario.startsWith("crash-")) await restartServer();
			gate.drop();
		}
		await firstAttempt;
		if (faultScenario !== "lost-cleanup-ack-response") {
			await refillPairing();
			await submitPairing();
		} else {
			await page.goto(`${baseURL}/admin/agents`);
			await page.getByText("active", { exact: true }).first().waitFor();
		}
	} else {
		await submitPairing();
	}
	const agentID = new URL(page.url()).pathname.split("/").at(-1);
	const durableAgentID = (await readOnly("SELECT id FROM agents LIMIT 1;")).trim();
	check(durableAgentID, "paired agent ID was not durable");
	const pairedAgentID = pairingScenarios.has(faultScenario) ? durableAgentID : agentID;
	check(pairedAgentID, "paired agent ID was not present in redirect");
	await page.goto(`${baseURL}/environments/new`);
	await page.locator('input[name="name"]').fill("Todo 12 browser environment");
	await Promise.all([
		page.waitForURL(`${baseURL}/environments`),
		page.getByRole("button", { name: "Create" }).click(),
	]);
	for (let attempt = 0; attempt < 100; attempt += 1) {
		await page.goto(`${baseURL}/admin/agents/${pairedAgentID}`);
		if ((await page.locator("body").innerText()).includes("active")) break;
		await new Promise((resolvePromise) => setTimeout(resolvePromise, 100));
	}
	const assignmentCard = page.getByRole("heading", { name: "Environment assignments" }).locator("..");
	await assignmentCard.getByText("Todo 12 browser environment").locator("..").getByRole("button", { name: "Assign" }).click();
	await page.waitForLoadState("networkidle");
	for (const width of [375, 768, 1280]) await capture(page, "agent-assigned", width);
	const state = await command("sqlite3", ["-readonly", database,
		`SELECT a.status||'|'||p.state||'|'||eaa.agent_id||'|'||eaa.environment_id FROM agents a JOIN agent_pairings p ON p.agent_id=a.id JOIN environment_agent_assignments eaa ON eaa.agent_id=a.id WHERE a.id='${pairedAgentID}';`]);
	check(state.trim().startsWith(`active|paired|${pairedAgentID}|`), `unexpected read-only assignment state: ${state.trim()}`);
	const environmentID = state.trim().split("|").at(-1);
	let lifecycleCheckpoint = null;
	if (lifecycle) {
		const agentIdentity = join(runDir, "agent-identity");
		await mkdir(agentIdentity, { mode: 0o700 });
		for (const file of ["identity.crt", "identity.key"]) {
			await command("podman", ["cp",
				`${currentAgentContainer}:/var/lib/durpdeploy-agent/${file}`,
				join(agentIdentity, file)]);
		}
		const token = (await command(serverBinary, ["tokens", "create", "--user", admin.email, "--name", "todo12-e2e"], { cwd: runDir, env: serverEnvironment })).trim();
		const api = async (method, path, body) => {
			const response = await fetch(`${baseURL}/api/v1${path}`, {
				method,
				headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
				body: body === undefined ? undefined : JSON.stringify(body),
				signal: AbortSignal.timeout(30000),
			});
			const text = await response.text();
			check(response.ok, `${method} ${path} returned ${response.status}: ${redact(text)}`);
			return text ? JSON.parse(text) : null;
		};
		if (isLifecycleScenario(faultScenario)) {
			lifecycleCheckpoint = await runLifecycleFault({
				agentStateDir: agentIdentity,
				api,
				command,
				environmentID,
				lifecycleAddress,
				lifecycleProxy,
				readOnly,
				restartAgent,
				scenario: faultScenario,
				serverIdentity,
				stopAgent,
			});
		} else {
		const project = await api("POST", "/projects", { name: "Todo 12 remote project" });
		await api("POST", `/projects/${project.id}/steps`, {
			name: "agent-only", sort_order: 1, timeout_seconds: 30, max_retries: 0,
			script_body: `if [ "$LANG" != "ddp-agent-${nonce}" ]; then echo SERVER_EXECUTION_MARKER; exit 91; fi\nprintf '%s\\n' 'AGENT_EXECUTION_MARKER:${nonce}' 'todo12-secret' 'remote-agent-ok'`,
		});
		const release = await api("POST", `/projects/${project.id}/releases`, { version: "todo12-v1" });
		const deployment = await api("POST", `/projects/${project.id}/deployments`, {
			release_id: release.id,
			environment_id: Number(environmentID),
		});
		const waitForStatus = async (deploymentID, expected) => {
			for (let attempt = 0; attempt < 300; attempt += 1) {
				if (agent.exitCode !== null) throw new Error(`agent exited ${agent.exitCode}: ${redact(agentErrors)}`);
				const status = await api("GET", `/deployments/${deploymentID}/status`);
				if (status.status === expected) return;
				if (["failed", "cancelled"].includes(status.status)) {
					const failedLogs = await command("sqlite3", ["-readonly", database,
						`SELECT 'claim|'||state||'|'||COALESCE(reason,'') FROM remote_deployment_claims WHERE deployment_id=${deploymentID} UNION ALL SELECT 'log|'||COALESCE(step_name,'')||'|'||line FROM deployment_logs WHERE deployment_id=${deploymentID};`]);
					throw new Error(`deployment ${deploymentID} became ${status.status}: ${redact(JSON.stringify(failedLogs))}`);
				}
				await new Promise((resolvePromise) => setTimeout(resolvePromise, 100));
			}
			throw new Error(`deployment ${deploymentID} did not finish`);
		};
		await waitForStatus(deployment.id, "succeeded");
		const logs = await api("GET", `/deployments/${deployment.id}/logs`);
		const lines = logs.map((entry) => entry.line);
		check(lines.join("\n").includes(`AGENT_EXECUTION_MARKER:${nonce}\n[REDACTED]\nremote-agent-ok`), `ordered redacted logs were ${JSON.stringify(lines)}`);
		check(!lines.some((line) => line.includes("SERVER_EXECUTION_MARKER") || line.includes("todo12-secret")), "execution or secret marker leaked");
		const failedProject = await api("POST", "/projects", { name: "Todo 12 retry project" });
		await api("POST", `/projects/${failedProject.id}/steps`, {
			name: "expected-failure", sort_order: 1, timeout_seconds: 30, max_retries: 0,
			script_body: "exit 23",
		});
		const failedRelease = await api("POST", `/projects/${failedProject.id}/releases`, { version: "todo12-fail-v1" });
		const failedDeployment = await api("POST", `/projects/${failedProject.id}/deployments`, {
			release_id: failedRelease.id,
			environment_id: Number(environmentID),
		});
		await waitForStatus(failedDeployment.id, "failed");
		const retry = await api("POST", `/deployments/${failedDeployment.id}/retry`);
		check(retry.id !== failedDeployment.id, "retry reused the source deployment ID");
		await waitForStatus(retry.id, "failed");
		const remoteState = await command("sqlite3", ["-readonly", database,
			`SELECT COUNT(*)||'|'||COUNT(DISTINCT agent_id) FROM remote_deployment_claims WHERE deployment_id IN (${failedDeployment.id},${retry.id}) AND state='failed';`]);
		check(remoteState.trim() === "2|1", `retry claims were ${remoteState.trim()}`);
		try {
			await command("test", ["!", "-e", sentinelMarker]);
		} catch (error) {
			throw new Error(`server Bash sentinel was invoked: ${error instanceof Error ? error.message : String(error)}`);
		}
		await writeFile(join(evidenceDir, "lifecycle.json"), `${JSON.stringify({
			agentMarker: `AGENT_EXECUTION_MARKER:${nonce}`,
			deploymentID: deployment.id,
			logs: lines,
			remoteClaims: remoteState.trim(),
			retryDeploymentID: retry.id,
			serverBashInvoked: false,
		}, null, 2)}\n`);
		}
	}
	await page.goto(`${baseURL}/admin/users/new`);
	await page.locator('input[name="email"]').fill(viewer.email);
	await page.locator('input[name="name"]').fill("Todo 12 Viewer");
	await page.locator('select[name="role"]').selectOption("viewer");
	await page.locator('input[name="password"]').fill(viewer.password);
	await page.locator('input[name="password_confirmation"]').fill(viewer.password);
	await Promise.all([
		page.waitForURL(`${baseURL}/admin/users`),
		page.getByRole("button", { name: "Create user" }).click(),
	]);
	await logout(page, baseURL);
	await login(page, baseURL, viewer);
	await page.goto(`${baseURL}/environments`);
	check(await page.getByRole("button", { name: /Assign|Unassign/ }).count() === 0, "viewer can see assignment controls");
	for (const width of [375, 768, 1280]) await capture(page, "viewer-environments", width);
	const csrf = await page.locator('meta[name="csrf-token"]').getAttribute("content");
	check(csrf, "viewer page lacks CSRF token");
	const denied = await page.evaluate(async ({ environmentID, agentID, csrf }) => {
		const response = await fetch(`/admin/environments/${environmentID}/agent`, {
			method: "PUT",
			headers: { "HX-Request": "true", "X-CSRF-Token": csrf, "Content-Type": "application/x-www-form-urlencoded" },
			body: new URLSearchParams({ agent_id: agentID }),
		});
		return { status: response.status, trigger: response.headers.get("HX-Trigger") };
	}, { environmentID, agentID: pairedAgentID, csrf });
	check(denied.status === 200 && denied.trigger?.includes("makeToast"), `viewer HTMX denial was ${JSON.stringify(denied)}`);
	await logout(page, baseURL);
	await login(page, baseURL, admin);
	await page.goto(`${baseURL}/admin/agents/${pairedAgentID}`);
	page.once("dialog", (dialog) => dialog.accept());
	await Promise.all([
		page.waitForResponse((response) => response.request().method() === "POST" && response.url().endsWith(`/admin/agents/${pairedAgentID}/revoke`)),
		page.getByRole("button", { name: "Revoke agent" }).click(),
	]);
	await page.goto(`${baseURL}/admin/agents`);
	check((await page.locator("body").innerText()).includes("revoked"), "revoked agent is not visible");
	const unexpectedConsoleErrors = consoleErrors.filter((message) =>
		!pairingScenarios.has(faultScenario) ||
		(!message.includes("422") && !message.includes("ERR_CONNECTION")));
	check(unexpectedConsoleErrors.length === 0,
		`browser console errors: ${unexpectedConsoleErrors.join("; ")}`);
	await writeFile(join(evidenceDir, "console.json"), `${JSON.stringify({ errors: unexpectedConsoleErrors }, null, 2)}\n`);
	await writeFile(join(evidenceDir, "network.filtered.json"), `${JSON.stringify(network, null, 2)}\n`);
	await writeFile(join(evidenceDir, "layout.json"), `${JSON.stringify(layout, null, 2)}\n`);
	await writeFile(join(evidenceDir, "proxy-transcript.json"), `${JSON.stringify({
		lifecycle: lifecycleProxy.transcript,
		pairing: pairingProxy.transcript,
	}, null, 2)}\n`);
	await writeFile(join(evidenceDir, "proof.json"), `${JSON.stringify({
		adminPairing: true,
		directAssignment: true,
		processes: {
			agent: await (async () => {
				if (agent.exitCode !== null || agent.signalCode !== null) {
					return { stopped: true };
				}
				const pid = Number((await command("podman", ["inspect", "--format", "{{.State.Pid}}", currentAgentContainer])).trim());
				return { exe: await readlink(`/proc/${pid}/exe`), pid };
			})(),
			server: { exe: await readlink(`/proc/${server.pid}/exe`), pid: server.pid },
		},
		readOnlyState: state.trim(),
		revoked: true,
		viewerControlsHidden: true,
		viewerHTMXToast: true,
		viewports: [375, 768, 1280],
	}, null, 2)}\n`);
	if (faultScenario) {
		await writeFile(join(evidenceDir, "fault.json"), `${JSON.stringify({
			lifecycle: lifecycleCheckpoint,
			checkpoint: pairingCheckpoint && {
				barrier: pairingCheckpoint.barrier,
				bytes: pairingCheckpoint.gate.bytes,
				direction: pairingCheckpoint.gate.direction,
				state: pairingCheckpoint.state,
			},
			scenario: faultScenario,
		}, null, 2)}\n`);
		console.log(`PASS ${faultScenario}`);
	}
}

try {
	await main();
} catch (error) {
	await mkdir(evidenceDir, { recursive: true });
	let durableState = "";
	try {
		durableState = await command("sqlite3", ["-readonly", database,
			"SELECT 'claim|'||state||'|'||COALESCE(reason,'') FROM remote_deployment_claims " +
			"UNION ALL SELECT 'log|'||COALESCE(step_name,'')||'|'||line FROM deployment_logs;"]);
	} catch (stateError) {
		durableState = `unavailable: ${stateError instanceof Error ? stateError.message : String(stateError)}`;
	}
	await writeFile(join(evidenceDir, "failure.json"), `${JSON.stringify({
		agentErrors: redact(agentErrors),
		durableState: redact(durableState),
		error: redact(error instanceof Error ? error.message : String(error)),
		proxies: {
			lifecycle: lifecycleProxy?.transcript,
			pairing: pairingProxy?.transcript,
		},
		serverErrors: redact(serverErrors),
	}, null, 2)}\n`);
	throw error;
} finally {
	if (browser) {
		await browser.close();
		cleanup.browserStopped = true;
	}
	if (agent) await command("podman", ["rm", "-f", "--ignore", currentAgentContainer]);
	await command("podman", ["volume", "rm", "-f", agentStateVolume]);
	await stop(agent, "agentStopped");
	await stop(server, "serverStopped");
	if (lifecycleProxy) await lifecycleProxy.close();
	if (pairingProxy) await pairingProxy.close();
	await rm(runDir, { recursive: true, force: true });
	cleanup.temporaryDirectoryRemoved = true;
	await mkdir(evidenceDir, { recursive: true });
	await writeFile(join(evidenceDir, "cleanup.json"), `${JSON.stringify(cleanup, null, 2)}\n`);
}
