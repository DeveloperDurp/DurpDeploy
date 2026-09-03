import { randomBytes } from "node:crypto";
import { promises as fs } from "node:fs";
import { spawn } from "node:child_process";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { chromium } from "playwright";

// allow: SIZE_OK — one sequential pairing proof; extract only for a second scenario.
const outputDir = process.env.AGENT_BROWSER_OUTPUT_DIR;
if (!outputDir) throw new Error("AGENT_BROWSER_OUTPUT_DIR is required");

const root = process.cwd();
const serverDir = await fs.mkdtemp(join(tmpdir(), "durpdeploy-agent-browser-"));
const binary = join(serverDir, "durpdeploy");
const agentBinary = join(serverDir, "durpdeploy-agent");
const database = join(serverDir, "durpdeploy.db");
const address = "127.0.0.1:18081";
const baseURL = `http://${address}`;
const agentAddress = "127.0.0.1:18082";
const agentURL = `https://${agentAddress}`;
const bootstrapAddress = "127.0.0.1:18083";
const bootstrapURL = `https://${bootstrapAddress}`;
const admin = { email: "admin@browser.test", password: "browser-admin-password" };
const consoleErrors = [];
const receipt = { agentStopped: false, serverDirectoryRemoved: false, serverStopped: false };

function run(command, args, options = {}) {
	return new Promise((resolve, reject) => {
		const child = spawn(command, args, { cwd: root, ...options });
		let stderr = "";
		child.stderr?.on("data", (chunk) => { stderr += chunk; });
		child.once("error", reject);
		child.once("exit", (code) => {
			if (code === 0) resolve();
			else reject(new Error(`${command} ${args.join(" ")} exited ${code}: ${stderr}`));
		});
	});
}

function runOutput(command, args, options = {}) {
	return new Promise((resolve, reject) => {
		const child = spawn(command, args, { cwd: root, ...options });
		let output = "";
		let errors = "";
		child.stdout.on("data", (chunk) => { output += chunk; });
		child.stderr.on("data", (chunk) => { errors += chunk; });
		child.once("error", reject);
		child.once("exit", (code) => {
			if (code === 0) resolve(output);
			else reject(new Error(`${command} ${args.join(" ")} exited ${code}: ${errors}`));
		});
	});
}

function start(command, args, options = {}) {
	return spawn(command, args, { cwd: root, stdio: "ignore", ...options });
}

function waitForPairingOffer(child) {
	return new Promise((resolve, reject) => {
		let output = "";
		let errors = "";
		const timeout = setTimeout(() => reject(new Error("agent did not print a pairing offer")), 10_000);
		child.stdout.on("data", (chunk) => {
			output += chunk;
			const code = output.match(/^Pairing code: (.+)$/m)?.[1];
			const fingerprint = output.match(/^Agent fingerprint: (.+)$/m)?.[1];
			if (code && fingerprint) {
				clearTimeout(timeout);
				resolve({ code, fingerprint });
			}
		});
		child.stderr.on("data", (chunk) => { errors += chunk; });
		child.once("error", (error) => {
			clearTimeout(timeout);
			reject(error);
		});
		child.once("exit", (code) => {
			clearTimeout(timeout);
			reject(new Error(`agent bootstrap exited ${code}: ${errors}`));
		});
	});
}

async function waitForHealth() {
	for (let attempt = 0; attempt < 100; attempt += 1) {
		try {
			if ((await fetch(`${baseURL}/healthz`)).ok) return;
		} catch {
			// The freshly-built server has not bound the port yet.
		}
		await new Promise((resolve) => setTimeout(resolve, 100));
	}
	throw new Error("browser proof server did not become healthy");
}

function assert(condition, message) {
	if (!condition) throw new Error(message);
}

async function saveJSON(name, value) {
	await fs.writeFile(join(outputDir, name), `${JSON.stringify(value, null, 2)}\n`);
}

function redactDiagnostic(value) {
	return value
		.replace(/ddp_pat_[\w-]+/g, "<redacted>")
		.replace(/(password|secret|token|claim)[=:]\S+/gi, "$1=<redacted>");
}

async function screenshot(page, name, options = {}) {
	await page.screenshot({ path: join(outputDir, name), fullPage: true, ...options });
}

async function checkPage(page, name) {
	const report = await page.evaluate(() => {
		const controls = [...document.querySelectorAll("a, button, input, select, textarea, summary")]
			.filter((element) => element.checkVisibility())
			.map((element) => {
					const label = (element.id
						? document.querySelector(`label[for="${CSS.escape(element.id)}"]`)?.textContent
						: "") || element.closest("label")?.textContent;
				return {
									element: element.tagName.toLowerCase(), id: element.id, name: element.getAttribute("name"),
					label: label || element.getAttribute("aria-label") || element.textContent?.trim() || "",
				};
			});
		const missingLabels = controls.filter((control) =>
			["input", "select", "textarea", "button", "summary"].includes(control.tag) && !control.label,
		);
		return {
			controls, missingLabels, statusText: [...document.querySelectorAll("[role=status], [role=alert], .badge")]
				.map((element) => element.textContent?.trim()).filter(Boolean),
			horizontalOverflow: document.documentElement.scrollWidth > document.documentElement.clientWidth,
		};
	});
	assert(report.missingLabels.length === 0, `${name} has unlabeled controls`);
	assert(!report.horizontalOverflow, `${name} has horizontal overflow`);
	return report;
}

async function tabOrder(page) {
	await page.locator("body").focus();
	const order = [];
	for (let index = 0; index < 8; index += 1) {
		await page.keyboard.press("Tab");
		order.push(await page.evaluate(() => {
			const element = document.activeElement;
			return element ? {
				element: element.tagName.toLowerCase(), id: element.id,
				label: element.getAttribute("aria-label") || element.textContent?.trim() || "",
			} : null;
		}));
	}
	assert(order.every((item) => item?.element), "keyboard focus order contains no focus target");
	return order;
}

let server;
let agent;
let browser;
try {
	await fs.mkdir(outputDir, { recursive: true });
	const environment = {
		...process.env,
		DURPDEPLOY_ADDR: address,
		DURPDEPLOY_AGENT_IDENTITY_DIR: join(serverDir, "server-identity"),
		DURPDEPLOY_AGENT_LISTEN_ADDR: agentAddress,
		DURPDEPLOY_AGENT_PUBLIC_URL: agentURL,
		DURPDEPLOY_DB: `${database}?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)`,
		DURPDEPLOY_SECRET_KEY: randomBytes(32).toString("base64"),
	};
	await run("go", ["build", "-o", binary, "./cmd/server"], { env: environment });
	await run("go", ["build", "-o", agentBinary, "github.com/DeveloperDurp/durpdeploy-agent/cmd/agent"], { env: environment });
	await run(binary, ["admin", "create", "--email", admin.email, "--password", admin.password], { env: environment });
	server = start(binary, [], { env: environment });
	await waitForHealth();
	agent = spawn(agentBinary, [], {
		cwd: root,
		stdio: ["ignore", "pipe", "pipe"],
		env: {
			...environment,
			DURPDEPLOY_AGENT_STATE_DIR: join(serverDir, "agent-state"),
			DURPDEPLOY_AGENT_LISTEN_ADDR: bootstrapAddress,
			DURPDEPLOY_AGENT_VERSION: "browser-v1",
		},
	});
	const pairing = await waitForPairingOffer(agent);

	browser = await chromium.launch({ headless: true });
	const context = await browser.newContext();
	context.on("page", (page) => {
		page.on("console", (message) => {
			if (message.type() === "error") consoleErrors.push(message.text());
		});
		page.on("pageerror", (error) => consoleErrors.push(error.message));
	});
	const page = await context.newPage();
	page.on("console", (message) => {
		if (message.type() === "error") consoleErrors.push(message.text());
	});
	page.on("pageerror", (error) => consoleErrors.push(error.message));

	await page.goto(`${baseURL}/login`);
	await page.getByLabel("Email").fill(admin.email);
	await page.getByLabel("Password").fill(admin.password);
	await page.getByRole("button", { name: "Login" }).click();
	await page.waitForLoadState("networkidle");
	assert(new URL(page.url()).pathname === "/", `login redirected to ${page.url()}`);

	await page.setViewportSize({ width: 1280, height: 768 });
	await page.goto(`${baseURL}/admin/agents`, { waitUntil: "networkidle" });
	const desktopAgents = await checkPage(page, "agents-desktop");
	await screenshot(page, "agents-desktop.png");
	await page.goto(`${baseURL}/admin/agents/new`, { waitUntil: "networkidle" });
	const newAgentFocus = await tabOrder(page);
	await screenshot(page, "agent-new-desktop.png");
	await page.getByLabel("Name").fill("Browser Agent");
	await page.getByLabel("Agent host or IP address").fill("127.0.0.1");
	await page.getByLabel(/Port/).fill("18083");
	await page.getByLabel("Pairing code").fill(pairing.code);
	await page.getByLabel("Agent fingerprint").fill(pairing.fingerprint);
	await page.getByRole("button", { name: "Create agent" }).click();
	await page.getByLabel("Type the agent fingerprint to confirm").waitFor();
	await page.getByLabel("Type the agent fingerprint to confirm").fill(pairing.fingerprint);
	await Promise.all([
		page.waitForURL(new RegExp(`${baseURL}/admin/agents/[^/]+$`)),
		page.getByRole("button", { name: "Confirm pairing" }).click(),
	]);
	const agentURLPath = new URL(page.url()).pathname;
	assert(agentURLPath.startsWith("/admin/agents/"), `pairing redirected to ${page.url()}`);
	const agentID = agentURLPath.slice("/admin/agents/".length);
	await checkPage(page, "agent-detail-desktop");
	await screenshot(page, "agent-detail-desktop.png");

	await page.goto(`${baseURL}/environments/new`, { waitUntil: "networkidle" });
	await page.getByLabel("Name", { exact: true }).fill("Browser remote environment");
	await page.getByRole("button", { name: "Create" }).click();
	await page.waitForURL(`${baseURL}/environments`);
	assert((await page.locator("body").innerText()).includes("Browser remote environment"), "environment form did not persist");

	for (let attempt = 0; attempt < 100; attempt += 1) {
		await page.goto(`${baseURL}/admin/agents`, { waitUntil: "networkidle" });
		if ((await page.locator("body").innerText()).includes("active")) break;
		await new Promise((resolve) => setTimeout(resolve, 100));
	}
	assert((await page.locator("body").innerText()).includes("active"), "agent did not poll after pairing");
	await screenshot(page, "agents-active-desktop.png");
	await page.goto(`${baseURL}/admin/agents/${agentID}`, { waitUntil: "networkidle" });
	await page.getByLabel("Environment", { exact: true }).selectOption({ label: "Browser remote environment" });
	await page.getByRole("button", { name: "Assign environment" }).click();
	await page.waitForLoadState("networkidle");
	assert((await page.locator("body").innerText()).includes("Browser remote environment"), "direct environment assignment did not persist");
	const assignmentDesktop = await checkPage(page, "agent-assignment-desktop");
	await screenshot(page, "agent-assignment-desktop.png");
	const databaseSummary = await runOutput("sqlite3", [database, `SELECT a.status, p.state, eaa.agent_id FROM agents a JOIN agent_pairings p ON p.agent_id = a.id JOIN environment_agent_assignments eaa ON eaa.agent_id = a.id WHERE a.id = '${agentID}';`]);
	assert(databaseSummary.trim() === `active|paired|${agentID}`, "database lacks the paired direct assignment");

	await page.setViewportSize({ width: 375, height: 812 });
	await page.goto(`${baseURL}/admin/agents`, { waitUntil: "networkidle" });
	const mobileAgents = await checkPage(page, "agents-mobile");
	await screenshot(page, "agents-mobile.png");
	await page.goto(`${baseURL}/admin/agents/${agentID}`, { waitUntil: "networkidle" });
	const assignmentMobile = await checkPage(page, "agent-assignment-mobile");
	await screenshot(page, "agent-assignment-mobile.png");
	assert(consoleErrors.length === 0, `browser console errors: ${consoleErrors.join("; ")}`);
	await saveJSON("viewport-metadata.json", {
		desktop: { width: 1280, height: 768, agents: desktopAgents, assignment: assignmentDesktop },
		mobile: { width: 375, height: 812, agents: mobileAgents, assignment: assignmentMobile },
	});
	await saveJSON("keyboard-navigation.json", { newAgentFocus });
	await saveJSON("listener-runtime.json", { agentPaired: true, pollObserved: true, publicURL: "configured" });
	await saveJSON("database-summary.json", { pairedDirectAssignment: databaseSummary.trim() });
	await saveJSON("browser-console.json", { errors: consoleErrors });
	} catch (error) {
		const diagnostics = {
			error: redactDiagnostic(error instanceof Error ? error.message : String(error)),
			consoleErrors: consoleErrors.map(redactDiagnostic),
		};
		await fs.mkdir(outputDir, { recursive: true });
		await saveJSON("browser-failure.json", diagnostics);
		console.error("browser proof diagnostics:", JSON.stringify(diagnostics));
		throw error;
} finally {
	if (browser) await browser.close();
	if (agent?.exitCode === null) {
		const exited = new Promise((resolve) => agent.once("exit", resolve));
		agent.kill("SIGTERM");
		await exited;
		receipt.agentStopped = true;
	}
	if (server?.exitCode === null) {
		const exited = new Promise((resolve) => server.once("exit", resolve));
		server.kill("SIGTERM");
		await exited;
		receipt.serverStopped = true;
	}
	await fs.rm(serverDir, { recursive: true, force: true });
	receipt.serverDirectoryRemoved = true;
	await fs.mkdir(outputDir, { recursive: true });
	await saveJSON("cleanup.json", receipt);
}
