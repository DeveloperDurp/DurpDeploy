import assert from "node:assert/strict";
import { createHash, randomBytes } from "node:crypto";
import { promises as fs } from "node:fs";
import { execFile, spawn } from "node:child_process";
import { promisify } from "node:util";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { chromium } from "playwright";
import { reserveHarnessAddresses, attachPageDiagnostics } from "./agent_admin_browser_proof_support.mjs";

const exec = promisify(execFile);
export async function until(check, description, timeout = 90000) {
	const deadline = Date.now() + timeout;
	do {
		const result = await check();
		if (result) return result;
		await new Promise((resolve) => setTimeout(resolve, 100));
	} while (Date.now() < deadline);
	throw new Error(`timed out: ${description}`);
}

export async function createRoutingHarness(outputDir) {
	await fs.mkdir(outputDir, { recursive: true });
	await fs.rm(join(outputDir, "result.json"), { force: true });
	const reservations = await reserveHarnessAddresses(Array(5).fill(undefined));
	const temp = await fs.mkdtemp(join(tmpdir(), "durpdeploy-routing-"));
	const database = join(temp, "proof.db");
	const binary = join(temp, "server");
	const agentBinary = join(temp, "agent");
	const [address, runtime, ...bootstrap] = reservations.addresses;
	const base = `http://${address}`;
	const password = randomBytes(24).toString("hex");
	const tokenPart = randomBytes(32).toString("hex");
	const token = `ddp_pat_${tokenPart}`;
	const secrets = [password, token];
	const processes = [];
	const errors = [];
	const env = { ...process.env, DURPDEPLOY_ADDR: address,
		DURPDEPLOY_DB: `${database}?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)`,
		DURPDEPLOY_AGENT_IDENTITY_DIR: join(temp, "identity"),
		DURPDEPLOY_AGENT_LISTEN_ADDR: runtime,
		DURPDEPLOY_AGENT_PUBLIC_URL: `https://${runtime}`,
		DURPDEPLOY_SECRET_KEY: randomBytes(32).toString("base64") };
	secrets.push(env.DURPDEPLOY_SECRET_KEY);
	let browser;
	let server;
	let sequence = 0;
	const onSignal = () => cleanup().finally(() => process.exit(1));
	process.once("SIGTERM", onSignal);
	process.once("SIGINT", onSignal);
	const deadline = setTimeout(onSignal, 900000);
	const redact = (text) => secrets.reduce((value, secret) => value.replaceAll(secret, "[redacted]"), text)
		.replace(/Pairing code: .*/g, "Pairing code: [redacted]")
		.replace(/ddp_pat_[\w-]+/g, "[redacted]");
	const save = async (name, data) => fs.writeFile(join(outputDir, name), redact(typeof data === "string" ? data : JSON.stringify(data, null, 2)));
	const sql = async (query) => (await exec("sqlite3", ["-cmd", ".timeout 5000", "-json", database, query], { timeout: 10000 })).stdout.trim();
	const rows = async (query) => JSON.parse((await sql(query)) || "[]");
	function start(name, command, childEnv) {
		const child = spawn(command, [], { cwd: temp, env: childEnv, stdio: ["ignore", "pipe", "pipe"] });
		const record = { name, child, output: "" };
		for (const stream of [child.stdout, child.stderr]) stream.on("data", (chunk) => {
			record.output = (record.output + chunk).slice(-1048576);
		});
		processes.push(record);
		return record;
	}
	async function stop(record) {
		if (record.child.exitCode !== null || record.child.signalCode !== null) return;
		const exited = new Promise((resolve) => record.child.once("exit", resolve));
		record.child.kill("SIGTERM");
		const timer = setTimeout(() => record.child.kill("SIGKILL"), 5000);
		await exited;
		clearTimeout(timer);
	}
	async function restart() {
		if (server) await stop(server);
		server = start(`server-${processes.length}`, binary, env);
		await until(async () => {
			assert.equal(server.child.exitCode, null, "server exited before readiness");
			try { return (await fetch(`${base}/healthz`, { signal: AbortSignal.timeout(2000) })).ok; }
			catch (error) { if (error.cause?.code === "ECONNREFUSED") return false; throw error; }
		}, "server health");
	}
	async function api(method, path, body, expected = 200) {
		const args = ["-sS", "-i", "--max-time", "15", "-X", method, "-H", `Authorization: Bearer ${token}`, "-H", "Content-Type: application/json"];
		if (body !== undefined) args.push("--data", JSON.stringify(body));
		args.push(`${base}/api/v1${path}`);
		const { stdout } = await exec("curl", args, { timeout: 20000, maxBuffer: 2097152 });
		await save(`http-${++sequence}.txt`, `${method} ${path}\n${stdout}`);
		const responseStatus = Number(stdout.match(/^HTTP\/\S+ (\d+)/)?.[1]);
		assert([expected].flat().includes(responseStatus), `${method} ${path}: ${stdout}`);
		const text = stdout.slice(stdout.indexOf("\r\n\r\n") + 4);
		const data = text ? JSON.parse(text) : null;
		return Array.isArray(expected) ? { status: responseStatus, data } : data;
	}
	async function cleanup() {
		clearTimeout(deadline);
		process.removeListener("SIGTERM", onSignal);
		process.removeListener("SIGINT", onSignal);
		if (browser) await browser.close();
		for (const record of [...processes].reverse()) {
			await stop(record);
			await save(`${record.name}.log`, record.output || "Process produced no diagnostic output.\n");
		}
		if (processes.length) await save("final-database.json", {
			deployments: await rows("SELECT id,status,parent_deployment_id,target_agent_id FROM deployments ORDER BY id;"),
			dispatches: await rows("SELECT deployment_id,state,reason FROM deployment_dispatches ORDER BY deployment_id;"),
		});
		await reservations.release();
		await fs.rm(temp, { recursive: true, force: true });
		await save("cleanup.json", { serverStopped: true, agentsStopped: processes.filter((p) => p.name.startsWith("agent")).length,
			browserClosed: true, tempRemoved: true, addresses: reservations.addresses,
			processes: processes.map(({ name, child }) => ({ name, pid: child.pid, exitCode: child.exitCode, signal: child.signalCode })) });
		const scanned = [];
		for (const name of await fs.readdir(outputDir)) {
			if (name.endsWith(".png")) continue;
			const contents = await fs.readFile(join(outputDir, name), "utf8");
			assert(!secrets.some((secret) => contents.includes(secret)), `secret in ${name}`);
			assert(!/-----BEGIN .*PRIVATE KEY-----/.test(contents), `private key in ${name}`);
			scanned.push(name);
		}
		await save("secret-scan.json", { passed: true, scanned });
	}
	try {
		await save("resources.json", { temp, database, addresses: reservations.addresses, ownsCache: false,
			serverBuildTags: "e2e", agentBuildTags: "agenttest", execution: "real bash with existing test-only OS sandbox bypass" });
		await exec("go", ["build", "-tags", "e2e", "-buildvcs=false", "-o", binary, "./cmd/server"], { timeout: 180000 });
		await exec("go", ["build", "-tags", "agenttest", "-buildvcs=false", "-o", agentBinary, "github.com/DeveloperDurp/durpdeploy-agent/cmd/agent"], { timeout: 180000 });
		await exec(binary, ["admin", "create", "--email", "admin@routing.test", "--password", password], { env, timeout: 30000 });
		await sql(`INSERT INTO api_tokens(id,user_id,name,token_prefix,token_hash) SELECT 'routing-proof',id,'routing-proof','proof','${createHash("sha256").update(tokenPart).digest("hex")}' FROM users WHERE email='admin@routing.test';`);
		await reservations.release();
		await restart();
		browser = await chromium.launch({ headless: true });
		const context = await browser.newContext();
		context.on("page", (page) => attachPageDiagnostics(page, errors));
		const page = await context.newPage();
		await page.goto(`${base}/login`);
		await page.getByLabel("Email").fill("admin@routing.test");
		await page.getByLabel("Password").fill(password);
		await page.getByRole("button", { name: "Login", exact: true }).click();
		await page.waitForURL(`${base}/`);
		const agents = [];
		for (const [index, bootstrapAddress] of bootstrap.entries()) {
			const record = start(`agent-${index + 1}`, agentBinary, { ...env,
				DURPDEPLOY_AGENT_STATE_DIR: join(temp, `agent-${index}`),
				DURPDEPLOY_AGENT_LISTEN_ADDR: bootstrapAddress, DURPDEPLOY_AGENT_VERSION: "routing-proof" });
			const offer = await until(() => {
				const code = record.output.match(/^Pairing code: (.+)$/m)?.[1];
				const fingerprint = record.output.match(/^Agent fingerprint: (.+)$/m)?.[1];
				return code && fingerprint ? { code, fingerprint } : false;
			}, "real agent pairing offer", 15000);
			secrets.push(offer.code);
			await page.goto(`${base}/admin/agents/new`);
			await page.getByLabel("Name").fill(`Cat Agent ${index + 1}`);
			await page.getByLabel("Agent host or IP address").fill("127.0.0.1");
			await page.getByLabel(/Port/).fill(new URL(`https://${bootstrapAddress}`).port);
			await page.getByLabel("Pairing code").fill(offer.code);
			await page.getByRole("button", { name: "Create agent" }).click();
			await page.getByLabel("Type the agent fingerprint to confirm").fill(offer.fingerprint);
			await page.getByRole("button", { name: "Confirm pairing" }).click();
			await page.waitForURL(/\/admin\/agents\/[^/]+$/);
			agents.push(new URL(page.url()).pathname.split("/").at(-1));
		}
		await until(async () => (await rows("SELECT a.id FROM agents a JOIN agent_pairings p ON p.agent_id=a.id WHERE a.status='active' AND p.state='paired' AND a.last_heartbeat_at IS NOT NULL;")).length === 3, "three real agents polling");
		await save("paired-agents.json", { agents, processes: processes.filter((p) => p.name.startsWith("agent")).map((p) => ({ name: p.name, pid: p.child.pid })) });
		return { api, save, rows, sql, page, base, agents, errors, restart, cleanup, outputDir };
	} catch (error) { await cleanup(); throw error; }
}
