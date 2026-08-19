import { createHash, randomBytes, X509Certificate } from "node:crypto";
import { promises as fs } from "node:fs";
import { spawn } from "node:child_process";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { chromium } from "playwright";

const outputDir = process.env.AGENT_BROWSER_EVIDENCE_DIR;
if (!outputDir) throw new Error("AGENT_BROWSER_EVIDENCE_DIR is required");

const root = process.cwd();
const serverDir = await fs.mkdtemp(join(tmpdir(), "durpdeploy-agent-browser-"));
const binary = join(serverDir, "durpdeploy");
const agentBinary = join(serverDir, "durpdeploy-agent");
const database = join(serverDir, "durpdeploy.db");
const address = "127.0.0.1:18081";
const baseURL = `http://${address}`;
const agentAddress = "127.0.0.1:18082";
const agentURL = `https://${agentAddress}`;
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

function start(command, args, options = {}) {
	return spawn(command, args, { cwd: root, stdio: "ignore", ...options });
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
					tag: element.tagName.toLowerCase(), id: element.id, name: element.getAttribute("name"),
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
				tag: element.tagName.toLowerCase(), id: element.id,
				label: element.getAttribute("aria-label") || element.textContent?.trim() || "",
			} : null;
		}));
	}
	assert(order.every((item) => item?.tag), "keyboard focus order contains no focus target");
	return order;
}

async function seedRemoteDeployment(poolID) {
	const sql = [
		"PRAGMA busy_timeout=5000;",
		"INSERT INTO projects (name) VALUES ('Browser remote project');",
		"INSERT INTO releases (project_id, version, steps_json) VALUES (1, 'browser-v1', '[]');",
		"INSERT INTO deployments (release_id, environment_id, status) VALUES (1, 1, 'pending');",
		`INSERT INTO deployment_dispatches (deployment_id, mode, pool_id, selector, state) VALUES (1, 'remote', ${poolID}, '', 'waiting');`,
	].join("\n");
	await run("sqlite3", [database, sql]);
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
	await run("go", ["build", "-o", agentBinary, "./cmd/agent"], { env: environment });
	await run(binary, ["admin", "create", "--email", admin.email, "--password", admin.password], { env: environment });
	server = start(binary, [], { env: environment });
	await waitForHealth();
	const serverFingerprint = createHash("sha256")
		.update(new X509Certificate(await fs.readFile(join(environment.DURPDEPLOY_AGENT_IDENTITY_DIR, "identity.crt"))).raw)
		.digest("hex");

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
	await page.getByLabel("Agent ID").fill("browser-agent");
	await page.getByLabel("Name").fill("Browser Agent");
	await page.getByLabel(/Agent version/).fill("browser-v1");
	await Promise.all([page.waitForURL(`${baseURL}/admin/agents/browser-agent`), page.getByRole("button", { name: "Create agent" }).click()]);
	await checkPage(page, "agent-detail-desktop");
	await screenshot(page, "agent-detail-desktop.png");

	await page.goto(`${baseURL}/admin/pools/new`, { waitUntil: "networkidle" });
	await page.getByLabel("Name").fill("Browser pool");
	await page.getByLabel("Description").fill("Container browser proof pool");
	await Promise.all([page.waitForURL(/\/admin\/pools\/\d+$/), page.getByRole("button", { name: "Create pool" }).click()]);
	const poolID = Number(new URL(page.url()).pathname.split("/").at(-1));
	assert(Number.isInteger(poolID) && poolID > 0, "pool form did not redirect to a pool detail");
	await checkPage(page, "pool-detail-desktop");
	await page.getByLabel("Add agent").selectOption("browser-agent");
	await page.getByRole("button", { name: "Add member" }).click();
	await page.waitForLoadState("networkidle");
	assert((await page.locator("body").innerText()).includes("Browser Agent"), "pool member form did not persist the agent");
	await screenshot(page, "pool-detail-desktop.png");
	await page.goto(`${baseURL}/admin/agents/browser-agent`, { waitUntil: "networkidle" });
	await page.getByLabel("Tag key").fill("region");
	await page.getByLabel("Tag value").fill("browser");
	await page.getByRole("button", { name: "Add tag" }).click();
	await page.waitForLoadState("networkidle");
	assert((await page.locator("body").innerText()).includes("region=browser"), "tag form did not persist the tag");
	await page.goto(`${baseURL}/environments/new`, { waitUntil: "networkidle" });
	await page.getByLabel("Name", { exact: true }).fill("Browser remote environment");
	await page.getByLabel("Remote agent pool").check();
	await page.getByLabel("Agent pool", { exact: true }).selectOption(String(poolID));
	await page.getByLabel("Required agent tags").fill("region=browser");
	await page.getByRole("button", { name: "Create" }).click();
	await page.waitForURL(`${baseURL}/environments`);
	assert((await page.locator("body").innerText()).includes("Browser remote environment"), "environment policy form did not persist the remote policy");

	await page.goto(`${baseURL}/admin/agents/browser-agent/enrollment`, { waitUntil: "networkidle" });
	await page.getByRole("button", { name: "Generate enrollment token" }).click();
	const token = await page.locator("#enrollment-token").textContent();
	assert(/^ddp_enroll_[0-9a-f]{64}$/.test(token ?? ""), "enrollment response did not contain a token");
	await screenshot(page, "agent-enrollment-redacted.png", { mask: [page.locator("#enrollment-token")] });
	await page.goto(`${baseURL}/admin/agents/browser-agent/enrollment`, { waitUntil: "networkidle" });
	assert((await page.locator("#enrollment-token").count()) === 0, "enrollment token remained after reload");
	assert(!(await page.locator("body").innerText()).includes(token), "enrollment token remained in reloaded text");
	agent = start(agentBinary, [], { env: {
		...environment,
		DURPDEPLOY_AGENT_ENROLLMENT_TOKEN: token,
		DURPDEPLOY_AGENT_ID: "browser-agent",
		DURPDEPLOY_AGENT_NAME: "Browser Agent",
		DURPDEPLOY_AGENT_SERVER_FINGERPRINT: serverFingerprint,
		DURPDEPLOY_AGENT_SERVER_URL: agentURL,
		DURPDEPLOY_AGENT_STATE_DIR: join(serverDir, "agent-state"),
		DURPDEPLOY_AGENT_VERSION: "browser-v1",
	} });
	for (let attempt = 0; attempt < 100; attempt += 1) {
		await page.goto(`${baseURL}/admin/agents`, { waitUntil: "networkidle" });
		if ((await page.locator("body").innerText()).includes("active")) break;
		await new Promise((resolve) => setTimeout(resolve, 100));
	}
	assert((await page.locator("body").innerText()).includes("active"), "agent did not enroll and poll the listener");
	await screenshot(page, "agents-active-desktop.png");
	await seedRemoteDeployment(poolID);

	await page.goto(`${baseURL}/deployments`, { waitUntil: "networkidle" });
	const deploymentDesktop = await checkPage(page, "deployments-desktop");
	assert((await page.locator("body").innerText()).includes("waiting"), "deployment list lacks remote waiting state");
	await screenshot(page, "deployments-desktop.png");
	await page.goto(`${baseURL}/deployments/1`, { waitUntil: "domcontentloaded" });
	const detailFocus = await tabOrder(page);
	assert((await page.locator("body").innerText()).includes("Browser pool"), "deployment detail lacks remote pool status");
	await screenshot(page, "deployment-remote-desktop.png");

	await page.setViewportSize({ width: 375, height: 812 });
	await page.goto(`${baseURL}/admin/agents`, { waitUntil: "networkidle" });
	const mobileAgents = await checkPage(page, "agents-mobile");
	await screenshot(page, "agents-mobile.png");
	await page.goto(`${baseURL}/admin/pools`, { waitUntil: "networkidle" });
	const mobilePools = await checkPage(page, "pools-mobile");
	await screenshot(page, "pools-mobile.png");
	await page.goto(`${baseURL}/deployments/1`, { waitUntil: "domcontentloaded" });
	const mobileDeployment = await checkPage(page, "deployment-mobile");
	await screenshot(page, "deployment-remote-mobile.png");
	assert(consoleErrors.length === 0, `browser console errors: ${consoleErrors.join("; ")}`);
	await saveJSON("viewport-metadata.json", {
		desktop: { width: 1280, height: 768, agents: desktopAgents, deployment: deploymentDesktop },
		mobile: { width: 375, height: 812, agents: mobileAgents, pools: mobilePools, deployment: mobileDeployment },
	});
	await saveJSON("keyboard-navigation.json", { newAgentFocus, detailFocus });
	await saveJSON("token-redaction.json", { initialResponse: true, screenshotMasked: true, absentAfterReload: true });
	await saveJSON("listener-runtime.json", { agentEnrolled: true, pollObserved: true, publicURL: "configured" });
	await saveJSON("browser-console.json", { errors: consoleErrors });
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
