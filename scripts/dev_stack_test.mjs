// Real two-worktree stacks, authenticated HTTPS, and ownership-safe teardown.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createHash, randomBytes, X509Certificate } from "node:crypto";
import { mkdtemp, mkdir, readFile, writeFile, copyFile, chmod, unlink, rename } from "node:fs/promises";
import https from "node:https";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { mintToken, passwordLogin } from "./mfa_browser_support.mjs";

const { chromium } = createRequire(import.meta.url)("playwright");
const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const launcher = join(root, "scripts/dev_stack.sh");
const threadLauncher = join(root, "scripts/dev_stack_thread.sh");
const runtime = process.env.DEV_STACK_RUNTIME || "podman";
const socket = process.env.DEV_STACK_SOCKET || (runtime === "podman"
	? `${process.env.XDG_RUNTIME_DIR || `/run/user/${process.getuid()}`}/podman/podman.sock`
	: "/var/run/docker.sock");
const engineArgs = runtime === "podman" ? ["--remote", `--url=unix://${socket}`] : [`--host=unix://${socket}`];
const env = { ...process.env, DEV_STACK_RUNTIME: runtime, DEV_STACK_SOCKET: socket };
// Retain these fixtures for manual API and browser checks, including on failure.
const directory = await mkdtemp(join(process.env.DEV_STACK_TEST_PARENT || tmpdir(), "durpdeploy-stack-test-"));
await chmod(directory, 0o700);
console.log(`Stack regression state: ${directory}`);
const worktrees = [];
const children = new Set();
let browser;
const completed = [];
let retainedStacks = [];
let fixtureImage;
let phase = "setup";
let failure;

function start(command, args, options = {}) {
	const child = spawn(command, args, { env, cwd: root, detached: true, ...options });
	children.add(child);
	let stdout = "";
	let stderr = "";
	child.stdout?.on("data", (chunk) => { stdout += chunk; });
	child.stderr?.on("data", (chunk) => { stderr += chunk; });
	const done = new Promise((resolveResult, reject) => {
		child.once("error", reject);
		child.once("close", (code, signal) => {
			children.delete(child);
			resolveResult({ code, signal, stdout, stderr });
		});
	});
	return { child, done };
}

async function run(command, args, options) {
	const result = await start(command, args, options).done;
	assert.equal(result.code, 0, `${command} failed (output retained privately)`);
	return result.stdout;
}

const engine = (...args) => run(runtime, [...engineArgs, ...args]);
const record = async (state) => JSON.parse(await readFile(join(state, "connection.json"), "utf8"));
const appName = (stack) => `${stack.stack_id}-app`;
const namespace = (stack) => `label=io.durpdeploy.namespace=${runtime}:${stack.stack_id}`;
const stackFilter = (stack) => `label=io.durpdeploy.dev-stack=${stack.stack_id}`;

async function launch(state, source, image, extraEnv = {}) {
	const result = await start(launcher, ["up", state], {
		env: { ...env, DEV_STACK_SOURCE: source, DEV_STACK_IMAGE: image, ...extraEnv },
	}).done;
	await writeFile(`${state}.startup.log`, result.stderr, { mode: 0o600 });
	assert.equal(result.code, 0, `Launch failed: ${state}.startup.log`);
	const stack = await record(state);
	assert.deepEqual(JSON.parse(result.stdout), stack, "Connection output must contain only the JSON record");
	return stack;
}

async function request(stack, path, { method = "GET", token, body, trusted = true, hostname } = {}) {
	const url = new URL(path, stack.url);
	if (hostname) url.hostname = hostname;
	const ca = trusted ? await readFile(stack.ca_file) : undefined;
	return new Promise((resolveResponse, reject) => {
		const req = https.request(url, {
			method, ca, lookup: (_host, options, callback) => options.all
				? callback(null, [{ address: "127.0.0.1", family: 4 }]) : callback(null, "127.0.0.1", 4),
			headers: { ...(token && { Authorization: `Bearer ${token}` }), ...(body && { "Content-Type": "application/json" }) },
		}, (res) => {
			let data = "";
			res.on("data", (chunk) => { data += chunk; });
			res.on("end", () => resolveResponse({ status: res.statusCode, headers: res.headers, data }));
		});
		req.setTimeout(5000, () => req.destroy(new Error("HTTPS deadline")));
		req.on("error", reject);
		req.end(body ? JSON.stringify(body) : undefined);
	});
}

async function api(stack, path, method = "GET", body, expected = 200) {
	const response = await request(stack, `/api/v1/${path}`, { method, body, token: stack.token });
	assert.equal(response.status, expected, `${method} ${path}: ${response.status}`);
	return JSON.parse(response.data);
}

async function bootstrap(stack) {
	const login = { email: "admin@stack.test", password: randomBytes(24).toString("hex") };
	await writeFile(join(stack.state_dir, "login.json"), JSON.stringify(login), { mode: 0o600 });
	await engine("exec", "--user=10001", appName(stack), "sh", "-c", `
		DURPDEPLOY_SECRET_KEY=$(cat /data/secret-key); export DURPDEPLOY_SECRET_KEY
		exec /usr/local/bin/container-entrypoint admin create --email "$1" --password "$2"`, "bootstrap", login.email, login.password);
	stack.login = login;
}

async function waitFor(check, message, attempts = 180) {
	for (let i = 0; i < attempts; i += 1) {
		if (await check()) return;
		await new Promise((resolveWait) => setTimeout(resolveWait, 250));
	}
	throw new Error(message);
}

async function healthy(stack) {
	await waitFor(async () => {
		try { return (await request(stack, "/healthz")).status === 200; } catch { return false; }
	}, "Stack did not recover");
}

async function assertGone(stack) {
	for (const kind of ["container", "volume", "network", "image"]) {
		const all = ["container", "image"].includes(kind) ? ["--all"] : [];
		assert.equal((await engine(kind, "ls", ...all, "--quiet", "--filter", stackFilter(stack))).trim(), "", `${kind} leaked`);
	}
	assert.equal((await engine("ps", "--all", "--quiet", "--filter", namespace(stack))).trim(), "");
	assert.equal((await engine("ps", "--all", "--quiet", "--filter", `${namespace(stack)}:gate-images`)).trim(), "");
	assert.equal((await engine("volume", "ls", "--quiet", "--filter", namespace(stack))).trim(), "");
}

async function deploy(stack) {
	const result = await api(stack, `projects/${stack.project.id}/deployments`, "POST", {
		release_id: stack.release.id, environment_id: stack.environment.id,
	}, 201);
	return result.id;
}

async function assertSucceeded(stack, deployment) {
	await waitFor(async () => {
		const result = await api(stack, `deployments/${deployment}/status`);
		if (["failed", "cancelled"].includes(result.status)) throw new Error(`Deployment ${deployment} ${result.status}`);
		return result.status === "succeeded";
	}, "Deployment did not complete");
}

try {
	for (const lane of ["a", "b"]) {
		const source = join(directory, `worktree-${lane}`);
		await run("git", ["worktree", "add", "--detach", "--lock", source, "HEAD"]);
		worktrees.push(source);
	}
	// Build once before concurrency budgets. Each launcher still owns its image tag/layer.
	let baseImage = process.env.DEV_STACK_IMAGE;
	if (!baseImage) {
		const warm = await launch(join(directory, "warm"), root, "");
		baseImage = `localhost/durpdeploy-stack-fixture:${randomBytes(12).toString("hex")}`;
		fixtureImage = baseImage;
		await engine("tag", `localhost/${warm.stack_id}:dev`, baseImage);
		await run(launcher, ["down", warm.state_dir]);
	}
	const [a, b] = await Promise.all(worktrees.map((source, i) => launch(join(directory, i ? "b" : "a"), source, baseImage)));
	retainedStacks = [a, b];
	assert.notEqual(a.stack_id, b.stack_id);
	assert.notEqual(new URL(a.url).port, new URL(b.url).port);
	assert.notEqual(new URL(a.agent_url).port, new URL(b.agent_url).port);
	assert.notEqual(await readFile(join(a.state_dir, "secret-key"), "utf8"), await readFile(join(b.state_dir, "secret-key"), "utf8"));
	for (const stack of [a, b]) {
		assert.equal((await request(stack, "/healthz")).status, 200);
		await assert.rejects(request(stack, "/healthz", { trusted: false }));
		await assert.rejects(request(stack, "/healthz", { hostname: "wrong.localhost" }), { code: "ERR_TLS_CERT_ALTNAME_INVALID" });
		await bootstrap(stack);
	}
	const pins = await Promise.all([a, b].map(async (stack) => {
		const cert = new X509Certificate(await readFile(join(stack.state_dir, "tls/cert.pem")));
		return createHash("sha256").update(cert.publicKey.export({ type: "spki", format: "der" })).digest("base64");
	}));
	// Trust only these two leaf keys, only inside this task-owned browser process.
	browser = await chromium.launch({ headless: true, args: [
		`--ignore-certificate-errors-spki-list=${pins.join(",")}`,
		"--host-resolver-rules=MAP *.localhost 127.0.0.1",
	] });
	for (const [index, stack] of [a, b].entries()) {
		const context = await browser.newContext();
		const page = await context.newPage();
		await passwordLogin(page, stack.url, stack.login.email, stack.login.password);
		assert.equal(new URL(page.url()).pathname, "/");
		stack.token = await mintToken(page, stack.url);
		const cookies = await context.cookies();
		assert(cookies.some((cookie) => cookie.secure && cookie.domain === new URL(stack.url).hostname));
		const sibling = index ? a : b;
		await page.goto(sibling.url);
		await page.waitForURL((url) => url.pathname === "/login");
		await page.goto(stack.url);
		await page.screenshot({ path: join(stack.state_dir, "authenticated.png"), fullPage: true });
		await context.close();
		stack.project = await api(stack, "projects", "POST", { name: `Stack ${index ? "B" : "A"}` }, 201);
		stack.environment = await api(stack, "environments", "POST", { name: "Isolated environment" }, 201);
		await api(stack, `projects/${stack.project.id}/steps`, "POST", {
			name: "Namespace isolation", interpreter: "bash", container_image: "harbor.durp.info/dockerhub/library/bash:5.2",
			script_body: 'set -eu\nprintf isolated > "$DURPDEPLOY_STAGE_DIR/probe"\nsleep 25\ntest "$(cat "$DURPDEPLOY_STAGE_DIR/probe")" = isolated\necho stack-deployment-ok',
		}, 201);
		stack.release = await api(stack, `projects/${stack.project.id}/releases`, "POST", { version: "stack-v1" }, 201);
		const configuration = JSON.parse(await engine("container", "inspect", appName(stack)))[0];
		assert(!configuration.NetworkSettings.Ports["8080/tcp"]);
		assert(configuration.Config.Env.includes(`DURPDEPLOY_CONTAINER_NAMESPACE=${stack.stack_id}`));
		assert(configuration.Config.Env.includes("TMPDIR=/data/tmp"));
		await engine("exec", "--user=10001", appName(stack), "test", "-d", "/data/tmp/durpdeploy-artifacts");
		stack.identity = await engine("exec", "--user=10001", appName(stack), "cat", "/var/lib/durpdeploy/agent-identity/identity.crt");
		const text = await run(launcher, ["status", stack.state_dir]);
		assert((await run(launcher, ["logs", stack.state_dir])).includes("server starting"));
		const proxyLogs = await start(launcher, ["logs", stack.state_dir, "caddy"]).done;
		assert.equal(proxyLogs.code, 0);
		assert((proxyLogs.stdout + proxyLogs.stderr).length > 0);
		for (const secret of [stack.login.password, stack.token, (await readFile(join(stack.state_dir, "secret-key"), "utf8")).trim()]) {
			assert(!text.includes(secret), "Status exposed a secret");
		}
	}
	assert.notEqual(a.identity, b.identity);
	assert.equal((await request(b, "/api/v1/projects", { token: a.token })).status, 401);
	const deployments = await Promise.all([a, b].map(deploy));
	await waitFor(async () => (await engine("ps", "--quiet", "--filter", namespace(b))).trim() !== "", "No B workload appeared");
	const bContainers = (await engine("ps", "--quiet", "--filter", namespace(b))).trim().split("\n");
	const bVolumes = (await engine("volume", "ls", "--quiet", "--filter", namespace(b))).trim().split("\n");
	assert(bVolumes.every(Boolean), "No B staging volume appeared");
	await engine("restart", appName(a));
	await healthy(a);
	for (const id of bContainers) await engine("container", "inspect", id);
	for (const id of bVolumes) await engine("volume", "inspect", id);
	await engine("run", "--detach", "--name", `${a.stack_id}-gate-probe`,
		"--label", `io.durpdeploy.namespace=${runtime}:${a.stack_id}:gate-images`,
		"--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges",
		"--user=65534:65534", "--memory=64m", "--cpus=0.25", "--pids-limit=32",
		"--entrypoint=bash", "harbor.durp.info/dockerhub/library/bash:5.2", "-c", "exec sleep 300");
	await run(launcher, ["down", a.state_dir]);
	await assertGone(a);
	await healthy(b);
	await assertSucceeded(b, deployments[1]);
	await assertSucceeded(b, await deploy(b));
	completed.push("Concurrent worktrees, HTTPS/API/browser isolation, restart, and teardown");
	console.log(`${completed.at(-1)}: PASS`);

	// Fail or interrupt after network/volumes/proxy exist, while B remains live.
	const tools = join(directory, "tools");
	await mkdir(tools);
	const binary = (await run("sh", ["-c", 'command -v "$1"', "runtime", runtime])).trim();
	await writeFile(join(tools, runtime), `#!/usr/bin/env bash
set -eu
if [[ $DEV_STACK_TEST_FAULT == image-inspect && " $* " == *' image inspect '* ]]; then
    count=0
    if [[ -f $DEV_STACK_TEST_GATE ]]; then read -r count < "$DEV_STACK_TEST_GATE"; fi
    count=$((count + 1))
    printf '%s\\n' "$count" > "$DEV_STACK_TEST_GATE"
    if ((count >= 3)); then echo 'Injected inspection failure' >&2; exit 42; fi
fi
if [[ $DEV_STACK_TEST_FAULT == stopped-inspect && " $* " == *' container inspect '* ]]; then
    echo 'Injected inspection failure' >&2
    exit 42
fi
if [[ ( $DEV_STACK_TEST_FAULT == fail || $DEV_STACK_TEST_FAULT == interrupt ) && " $* " == *' create '* && " $* " == *'-app '* ]]; then
    touch "$DEV_STACK_TEST_GATE"
    if [[ $DEV_STACK_TEST_FAULT == interrupt ]]; then sleep 60; fi
    exit 42
fi
exec "$DEV_STACK_TEST_ENGINE" "$@"
`, { mode: 0o700 });
	for (const fault of ["fail", "interrupt"]) {
		phase = `${fault}:startup`;
		const state = join(directory, fault);
		const gate = join(directory, `${fault}.gate`);
		const task = start(launcher, ["up", state], { env: {
			...env, DEV_STACK_IMAGE: baseImage, PATH: `${tools}:${env.PATH}`,
			DEV_STACK_TEST_GATE: gate, DEV_STACK_TEST_FAULT: fault, DEV_STACK_TEST_ENGINE: binary,
		} });
		if (fault === "interrupt") {
			await waitFor(async () => readFile(gate).then(() => true, () => false), "Startup did not reach interruption gate");
			process.kill(-task.child.pid, "SIGTERM");
		}
		const result = await task.done;
		await writeFile(`${state}.startup.log`, result.stderr, { mode: 0o600 });
		assert.notEqual(result.code, 0);
		phase = `${fault}:cleanup`;
		await assertGone(await record(state));
		await healthy(b);
	}
	for (const fault of ["image-inspect", "stopped-inspect"]) {
		phase = `${fault}:launch`;
		const state = join(directory, fault);
		const stack = await launch(state, worktrees[0], baseImage);
		if (fault === "stopped-inspect") await engine("stop", "--time=30", appName(stack));
		const result = await start(launcher, ["down", state], { env: {
			...env, PATH: `${tools}:${env.PATH}`, DEV_STACK_TEST_FAULT: fault,
			DEV_STACK_TEST_GATE: join(directory, `${fault}.counter`), DEV_STACK_TEST_ENGINE: binary,
		} }).done;
		await writeFile(`${state}.cleanup-probe.log`, result.stderr, { mode: 0o600 });
		phase = `${fault}:refusal`;
		assert.notEqual(result.code, 0, "Inspection errors must not confirm cleanup");
		assert.notEqual((await record(state)).status, "down");
		if (fault === "image-inspect") {
			await engine("image", "inspect", `localhost/${stack.stack_id}:dev`);
		} else {
			await engine("container", "inspect", appName(stack));
			await engine("container", "inspect", `${stack.stack_id}-caddy`);
		}
		await healthy(b);
		phase = `${fault}:retry`;
		await run(launcher, ["down", state]);
		await assertGone(stack);
	}
	completed.push("Inspection errors remain unconfirmed and cleanup retries succeed");
	console.log(`${completed.at(-1)}: PASS`);
	assert.notEqual((await start(launcher, ["up", b.state_dir]).done).code, 0, "Replaced a live stack");
	const copied = join(directory, "copied");
	await mkdir(copied);
	await copyFile(join(b.state_dir, "connection.json"), join(copied, "connection.json"));
	assert.notEqual((await start(launcher, ["down", copied]).done).code, 0, "Accepted copied ownership");
	// An altered ownership label must stop cleanup before it removes anything.
	const original = await readFile(join(b.state_dir, "connection.json"), "utf8");
	await writeFile(join(b.state_dir, "connection.json"), JSON.stringify({ ...JSON.parse(original), run_id: "0".repeat(32) }));
	try { assert.notEqual((await start(launcher, ["down", b.state_dir]).done).code, 0); }
	finally { await writeFile(join(b.state_dir, "connection.json"), original); }
	const altered = JSON.parse(original);
	altered.ids[appName(b)] = "0".repeat(64);
	await writeFile(join(b.state_dir, "connection.json"), JSON.stringify(altered));
	try { assert.notEqual((await start(launcher, ["down", b.state_dir]).done).code, 0); }
	finally { await writeFile(join(b.state_dir, "connection.json"), original); }
	await healthy(b);
	phase = "worktree:settle";
	const configuration = JSON.parse(await readFile(join(root, "t3.json"), "utf8"));
	const hooks = configuration.scripts.filter((script) => script.runOnSettle);
	assert.equal(hooks.length, 1);
	const threadEnv = {
		...env, DEV_STACK_IMAGE: baseImage, XDG_STATE_HOME: join(directory, "thread-state"),
		T3CODE_WORKTREE_PATH: worktrees[0],
	};
	const threadOptions = { cwd: worktrees[0], env: threadEnv };
	const settle = (extraEnv = {}) => start("bash", ["-c", hooks[0].command], {
		...threadOptions, env: { ...threadEnv, ...extraEnv },
	});
	const registration = join(worktrees[0], ".local/dev-stack.json");
	const readRegistration = async () => JSON.parse(await readFile(registration, "utf8"));
	assert.equal((await settle().done).code, 0, "Unused worktree settlement failed");
	const siblingOptions = { cwd: worktrees[1], env: { ...threadEnv, T3CODE_WORKTREE_PATH: worktrees[1] } };
	const siblingRegistration = join(worktrees[1], ".local/dev-stack.json");
	const siblingWrapper = join(worktrees[1], "scripts/dev_stack_thread.sh");
	await rename(siblingWrapper, `${siblingWrapper}.held`);
	try {
		assert.equal((await start("bash", ["-c", hooks[0].command], siblingOptions).done).code, 0);
		await mkdir(dirname(siblingRegistration), { recursive: true, mode: 0o700 });
		await writeFile(siblingRegistration, "{}");
		assert.notEqual((await start("bash", ["-c", hooks[0].command], siblingOptions).done).code, 0);
		await unlink(siblingRegistration);
	} finally { await rename(`${siblingWrapper}.held`, siblingWrapper); }
	assert.notEqual((await start("bash", [threadLauncher, "up"], {
		...threadOptions, env: { ...threadEnv, DEV_STACK_RUNTIME: "unsupported" },
	}).done).code, 0);
	await assert.rejects(readRegistration(), { code: "ENOENT" });
	const failedThread = await start("bash", [threadLauncher, "up"], {
		...threadOptions, env: {
			...threadEnv, PATH: `${tools}:${env.PATH}`, DEV_STACK_TEST_FAULT: "fail",
			DEV_STACK_TEST_GATE: join(directory, "thread-fail.gate"), DEV_STACK_TEST_ENGINE: binary,
		},
	}).done;
	await writeFile(join(directory, "thread-fail.log"), failedThread.stderr, { mode: 0o600 });
	assert.notEqual(failedThread.code, 0);
	const failedThreadStack = await record((await readRegistration()).state_dir);
	assert.equal(failedThreadStack.status, "down");
	assert.equal((await settle().done).code, 0);
	await assertGone(failedThreadStack);
	await assert.rejects(readRegistration(), { code: "ENOENT" });
	const thread = JSON.parse(await run("bash", [threadLauncher, "up"], threadOptions));
	const originalRegistration = await readFile(registration, "utf8");
	assert.notEqual((await start("bash", [threadLauncher, "up"], threadOptions).done).code, 0);
	assert.equal(await readFile(registration, "utf8"), originalRegistration);
	const lockGate = join(directory, "thread-lock.gate");
	const lock = start("flock", ["--no-fork", `${registration}.lock`, "sh", "-c",
		'touch "$1"; exec sleep 60', "lock", lockGate], threadOptions);
	await waitFor(async () => readFile(lockGate).then(() => true, () => false), "Registration lock was not acquired");
	try { assert.notEqual((await settle().done).code, 0, "Concurrent settlement bypassed the lock"); }
	finally { process.kill(-lock.child.pid, "SIGTERM"); await lock.done; }
	await bootstrap(thread);
	const cert = new X509Certificate(await readFile(join(thread.state_dir, "tls/cert.pem")));
	const pin = createHash("sha256").update(cert.publicKey.export({ type: "spki", format: "der" })).digest("base64");
	const threadBrowser = await chromium.launch({ headless: true, args: [
		`--ignore-certificate-errors-spki-list=${pin}`,
		"--host-resolver-rules=MAP *.localhost 127.0.0.1",
	] });
	try {
		const page = await threadBrowser.newPage();
		await passwordLogin(page, thread.url, thread.login.email, thread.login.password);
		thread.token = await mintToken(page, thread.url);
		await page.screenshot({ path: join(directory, "thread-authenticated.png"), fullPage: true });
		const response = await request(thread, "/api/v1/projects", { token: thread.token });
		assert.equal(response.status, 200);
		assert.equal(JSON.parse(response.data).items.length, 0);
		await writeFile(join(directory, "thread-api.json"), JSON.stringify({
			method: "GET", url: `${thread.url}/api/v1/projects`, ...response,
		}, null, 2));
	} finally { await threadBrowser.close(); }
	assert.equal(JSON.parse(await run("bash", [threadLauncher, "status"], threadOptions)).container_state, "running");
	assert((await run("bash", [threadLauncher, "logs"], threadOptions)).includes("server starting"));
	const copiedRegistration = join(worktrees[1], ".local/dev-stack.json");
	await mkdir(dirname(copiedRegistration), { recursive: true, mode: 0o700 });
	await copyFile(registration, copiedRegistration);
	assert.notEqual((await start("bash", ["-c", hooks[0].command], {
		cwd: worktrees[1], env: { ...threadEnv, T3CODE_WORKTREE_PATH: worktrees[1] },
	}).done).code, 0, "Copied registration authorized another worktree");
	await unlink(copiedRegistration);
	assert.notEqual((await settle({ T3CODE_WORKTREE_PATH: worktrees[1] }).done).code, 0);
	const otherThread = JSON.parse(await run("bash", [threadLauncher, "up"], siblingOptions));
	const otherConnection = await readFile(join(otherThread.state_dir, "connection.json"), "utf8");
	await writeFile(registration, JSON.stringify({ ...await readRegistration(), state_dir: otherThread.state_dir }));
	await writeFile(join(otherThread.state_dir, "connection.json"), JSON.stringify({ ...JSON.parse(otherConnection), source_dir: worktrees[0] }));
	try { assert.notEqual((await settle().done).code, 0, "Edited JSON bypassed the resource worktree label"); }
	finally {
		await writeFile(join(otherThread.state_dir, "connection.json"), otherConnection);
		await writeFile(registration, originalRegistration);
	}
	await healthy(otherThread);
	await healthy(thread);
	assert.equal((await start("bash", ["-c", hooks[0].command], siblingOptions).done).code, 0);
	await assertGone(otherThread);
	await writeFile(registration, JSON.stringify({ ...await readRegistration(), state_dir: b.state_dir }));
	try { assert.notEqual((await settle().done).code, 0, "Foreign connection authorized cleanup"); }
	finally { await writeFile(registration, originalRegistration); }
	const missingConnection = await readFile(join(thread.state_dir, "connection.json"), "utf8");
	await unlink(join(thread.state_dir, "connection.json"));
	try { assert.notEqual((await settle().done).code, 0, "Missing record confirmed cleanup"); }
	finally { await writeFile(join(thread.state_dir, "connection.json"), missingConnection); }
	const refusedSettle = await settle({
		PATH: `${tools}:${env.PATH}`, DEV_STACK_TEST_FAULT: "image-inspect",
		DEV_STACK_TEST_GATE: join(directory, "thread-inspect.counter"), DEV_STACK_TEST_ENGINE: binary,
	}).done;
	await writeFile(join(directory, "thread-inspect.log"), refusedSettle.stderr, { mode: 0o600 });
	assert.notEqual(refusedSettle.code, 0);
	assert.equal(await readFile(registration, "utf8"), originalRegistration);
	assert.notEqual((await record(thread.state_dir)).status, "down");
	assert.equal((await settle().done).code, 0);
	await assertGone(thread);
	await assert.rejects(readRegistration(), { code: "ENOENT" });
	assert.equal((await settle().done).code, 0, "Repeated settlement failed");
	await healthy(b);
	const siblingResponse = await request(b, "/api/v1/projects", { token: b.token });
	assert.equal(siblingResponse.status, 200);
	assert(JSON.parse(siblingResponse.data).items.some((project) => project.id === b.project.id));
	await writeFile(join(directory, "thread-sibling-api.json"), JSON.stringify({
		method: "GET", url: `${b.url}/api/v1/projects`, ...siblingResponse,
	}, null, 2));
	const siblingPage = await browser.newPage();
	await passwordLogin(siblingPage, b.url, b.login.email, b.login.password);
	await siblingPage.screenshot({ path: join(directory, "thread-sibling.png"), fullPage: true });
	await siblingPage.close();
	completed.push("T3 worktree settlement, ownership rejection, cleanup retry, and retained fixture isolation");
	console.log(`${completed.at(-1)}: PASS`);
	const retained = await launch(join(directory, "a-retained"), worktrees[0], baseImage);
	await bootstrap(retained);
	retainedStacks = [retained, b];
	completed.push("Partial failure, interrupted startup, collision, and ownership rejection");
	console.log(`${completed.at(-1)}: PASS`);
	for (const stack of [retained, b]) console.log(`Retained stack: ${stack.url} state=${stack.state_dir}`);
	console.log(`Repeat and teardown: docs/manual-e2e.md (stack regression state: ${directory})`);
} catch (error) {
	// Publish only controlled phase names and source locations, never error text.
	failure = { phase, locations: [...String(error.stack).matchAll(/\bdev_stack_test\.mjs:\d+:\d+/g)].map((match) => match[0]) };
	// Browser and subprocess errors can contain credentials. Retain diagnostics privately.
	await writeFile(join(directory, "failure.txt"), String(error.stack), { mode: 0o600 });
	console.error(`Stack regression: FAIL (private diagnostics: ${directory}/failure.txt)`);
	process.exitCode = 1;
} finally {
	for (const child of children) {
		try { process.kill(-child.pid, "SIGTERM"); } catch { /* Already exited. */ }
	}
	await browser?.close();
	if (fixtureImage) await engine("image", "rm", "--no-prune", fixtureImage);
	if (process.env.DEV_STACK_EVIDENCE_DIR) {
		const evidence = process.env.DEV_STACK_EVIDENCE_DIR;
		await mkdir(evidence, { recursive: true });
		const stacks = await Promise.all(retainedStacks.map((stack) => record(stack.state_dir)));
		await writeFile(join(evidence, "result.json"), JSON.stringify({
			status: process.exitCode ? "FAIL" : "PASS", completed, failure, stacks,
		}, null, 2));
		for (const [i, lane] of ["a", "b"].entries()) {
			try { await copyFile(join(directory, lane, "authenticated.png"), join(evidence, `authenticated-${i}.png`)); }
			catch (error) { if (error.code !== "ENOENT") throw error; }
		}
		for (const name of ["thread-authenticated.png", "thread-sibling.png", "thread-api.json", "thread-sibling-api.json"]) {
			try { await copyFile(join(directory, name), join(evidence, name)); }
			catch (error) { if (error.code !== "ENOENT") throw error; }
		}
	}
	// Keep real stacks/data, but release only our own clean, detached source worktrees.
	for (const source of worktrees) {
		await run("git", ["worktree", "unlock", source]);
		await run("git", ["worktree", "remove", source]);
	}
}
