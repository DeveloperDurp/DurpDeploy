import { randomBytes } from "node:crypto";
import { promises as fs } from "node:fs";
import { spawn } from "node:child_process";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { chromium } from "playwright";
import {
	attachPageDiagnostics,
	consoleErrorTexts,
	findUnexpectedConsoleErrors,
	inspectResponsiveBounds,
	isExplicitlyEmptyResponse,
	reserveHarnessAddresses,
	waitForSettledRename,
} from "./agent_admin_browser_proof_support.mjs";

// allow: SIZE_OK — one sequential pairing proof; extract only for a second scenario.
const outputDir = process.env.AGENT_BROWSER_OUTPUT_DIR;
if (!outputDir) throw new Error("AGENT_BROWSER_OUTPUT_DIR is required");
const scenario = process.argv.includes("--scenario")
	? process.argv[process.argv.indexOf("--scenario") + 1]
	: "pairing";

if (scenario === "full-routing") {
	const { runFullRoutingOrchestration } = await import("./agent_full_routing_orchestration.mjs");
	await runFullRoutingOrchestration(outputDir);
	process.exit(0);
}

if (scenario === "fanout") {
	const { runFanoutProof } = await import("./agent_fanout_browser_proof.mjs");
	await runFanoutProof(outputDir);
	process.exit(0);
}

const root = process.cwd();
const serverDir = await fs.mkdtemp(join(tmpdir(), "durpdeploy-agent-browser-"));
const binary = join(serverDir, "durpdeploy");
const agentBinary = join(serverDir, "durpdeploy-agent");
const database = join(serverDir, "durpdeploy.db");
let address;
let baseURL;
let agentAddress;
let agentURL;
let bootstrapAddress;
let bootstrapURL;
const admin = { email: "admin@browser.test", password: "browser-admin-password" };
const deployer = { email: "deployer@browser.test", password: "browser-deployer-password" };
const viewer = { email: "viewer@browser.test", password: "browser-viewer-password" };
const consoleErrors = [];
const expectedHTTPFailures = [];
let serverErrors = "";
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
		if (server?.exitCode !== null) {
			await new Promise((resolve) => setTimeout(resolve, 20));
			throw new Error(`browser proof server exited ${server.exitCode}: ${serverErrors}`);
		}
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
		const responsiveElements = [...document.querySelectorAll(
			"[data-proof-bounds], [data-proof-touch]",
		)].filter((element) => element.checkVisibility()).map((element) => {
			const rect = element.getBoundingClientRect().toJSON();
			let clipRect;
			for (let ancestor = element.parentElement; ancestor; ancestor = ancestor.parentElement) {
				const style = getComputedStyle(ancestor);
				if (![style.overflowX, style.overflowY].some((value) =>
					["auto", "clip", "hidden", "scroll"].includes(value))) continue;
				const bounds = ancestor.getBoundingClientRect();
				clipRect = clipRect ? {
					left: Math.max(clipRect.left, bounds.left),
					top: Math.max(clipRect.top, bounds.top),
					right: Math.min(clipRect.right, bounds.right),
					bottom: Math.min(clipRect.bottom, bounds.bottom),
				} : bounds.toJSON();
			}
			return {
				label: element.textContent?.trim() || element.getAttribute("aria-label") || element.tagName,
				touch: element.hasAttribute("data-proof-touch"), rect, clipRect,
			};
		});
		return {
			controls, missingLabels, statusText: [...document.querySelectorAll("[role=status], [role=alert], .badge")]
				.map((element) => element.textContent?.trim()).filter(Boolean),
			horizontalOverflow: document.documentElement.scrollWidth > document.documentElement.clientWidth,
			responsiveElements,
			viewport: { left: 0, top: 0, right: innerWidth, bottom: innerHeight },
		};
	});
	const bounds = inspectResponsiveBounds(report.responsiveElements, report.viewport);
	report.clippedElements = bounds.clipped;
	report.undersizedTouchTargets = bounds.undersizedTouchTargets;
	assert(report.missingLabels.length === 0, `${name} has unlabeled controls`);
	assert(!report.horizontalOverflow, `${name} has horizontal overflow`);
	assert(report.clippedElements.length === 0,
		`${name} has clipped responsive elements: ${JSON.stringify(report.clippedElements)}`);
	assert(report.undersizedTouchTargets.length === 0,
		`${name} has undersized touch targets: ${JSON.stringify(report.undersizedTouchTargets)}`);
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
let addressReservations;
try {
	await fs.mkdir(outputDir, { recursive: true });
	addressReservations = await reserveHarnessAddresses([
		process.env.AGENT_BROWSER_SERVER_ADDR,
		process.env.AGENT_BROWSER_RUNTIME_ADDR,
		process.env.AGENT_BROWSER_BOOTSTRAP_ADDR,
	]);
	[address, agentAddress, bootstrapAddress] = addressReservations.addresses;
	baseURL = `http://${address}`;
	agentURL = `https://${agentAddress}`;
	bootstrapURL = `https://${bootstrapAddress}`;
	const environment = {
		...process.env,
		DURPDEPLOY_ADDR: address,
		DURPDEPLOY_AGENT_IDENTITY_DIR: join(serverDir, "server-identity"),
		DURPDEPLOY_AGENT_LISTEN_ADDR: agentAddress,
		DURPDEPLOY_AGENT_PUBLIC_URL: agentURL,
		DURPDEPLOY_DB: `${database}?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)`,
		DURPDEPLOY_SECRET_KEY: randomBytes(32).toString("base64"),
	};
	await run("go", ["build", "-buildvcs=false", "-o", binary, "./cmd/server"], { env: environment });
	await run("go", ["build", "-buildvcs=false", "-o", agentBinary, "github.com/DeveloperDurp/durpdeploy-agent/cmd/agent"], { env: environment });
	await run(binary, ["admin", "create", "--email", admin.email, "--password", admin.password], { env: environment });
	if (scenario === "labels") {
		await run(binary, ["admin", "create", "--email", deployer.email, "--password", deployer.password], { env: environment });
		await run(binary, ["admin", "create", "--email", viewer.email, "--password", viewer.password], { env: environment });
		await run("sqlite3", [database, `
UPDATE users SET role = 'deployer' WHERE email = '${deployer.email}';
UPDATE users SET role = 'viewer' WHERE email = '${viewer.email}';
INSERT INTO agents (id, name, status, certificate_pem, certificate_fingerprint) VALUES
 ('label-agent-1', 'Label Agent One', 'active', 'cert-1', '1111111111111111111111111111111111111111111111111111111111111111'),
 ('label-agent-2', 'Label Agent Two', 'active', 'cert-2', '2222222222222222222222222222222222222222222222222222222222222222'),
 ('label-agent-3', 'Label Agent Three', 'active', 'cert-3', '3333333333333333333333333333333333333333333333333333333333333333'),
 ('label-unpaired', 'Label Unpaired', 'active', 'cert-4', '4444444444444444444444444444444444444444444444444444444444444444');
INSERT INTO agent_pairings (
 agent_id, pairing_code_hash, agent_public_identity, agent_pin,
 server_public_identity, server_pin, state, expires_at, paired_at
) VALUES
 ('label-agent-1', X'0101010101010101010101010101010101010101010101010101010101010101', 'public-1', 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1', 'server-1', 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb1', 'paired', 2000000000, 1000000000),
 ('label-agent-2', X'0202020202020202020202020202020202020202020202020202020202020202', 'public-2', 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa2', 'server-2', 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb2', 'paired', 2000000000, 1000000000),
 ('label-agent-3', X'0303030303030303030303030303030303030303030303030303030303030303', 'public-3', 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa3', 'server-3', 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb3', 'paired', 2000000000, 1000000000);`]);
		}
	await addressReservations.release();
	addressReservations = undefined;
	server = spawn(binary, [], {
		cwd: root, stdio: ["ignore", "pipe", "pipe"], env: environment,
	});
	server.stdout.on("data", (chunk) => { serverErrors += chunk; });
	server.stderr.on("data", (chunk) => { serverErrors += chunk; });
	await waitForHealth();
	if (scenario !== "labels") agent = spawn(agentBinary, [], {
		cwd: root,
		stdio: ["ignore", "pipe", "pipe"],
		env: {
			...environment,
			DURPDEPLOY_AGENT_STATE_DIR: join(serverDir, "agent-state"),
			DURPDEPLOY_AGENT_LISTEN_ADDR: bootstrapAddress,
			DURPDEPLOY_AGENT_VERSION: "browser-v1",
		},
	});
	const pairing = scenario === "labels" ? null : await waitForPairingOffer(agent);

	browser = await chromium.launch({ headless: true });
	const context = await browser.newContext();
	context.on("page", (newPage) => attachPageDiagnostics(newPage, consoleErrors));
	const page = await context.newPage();
	attachPageDiagnostics(page, consoleErrors);

	await page.goto(`${baseURL}/login`);
	await page.getByLabel("Email").fill(admin.email);
	await page.getByLabel("Password").fill(admin.password);
	await page.getByRole("button", { name: "Login" }).click();
	await page.waitForLoadState("networkidle");
	assert(new URL(page.url()).pathname === "/", `login redirected to ${page.url()}`);
	if (scenario === "labels") {
		await page.setViewportSize({ width: 1280, height: 768 });
		await page.goto(`${baseURL}/admin/agent-labels`, { waitUntil: "networkidle" });
		await page.getByLabel("Name", { exact: true }).fill("  Cat Fact  ");
		await page.getByRole("button", { name: "Create label" }).click();
		await page.waitForURL(/\/admin\/agent-labels\/\d+$/);
		const labelPath = new URL(page.url()).pathname;
		const labelID = labelPath.split("/").at(-1);
		for (const agentName of ["Label Agent One", "Label Agent Two", "Label Agent Three"]) {
			await page.getByLabel("Active paired agent").selectOption({ label: `${agentName} (active)` });
			await page.getByRole("button", { name: "Add member" }).click();
			await page.waitForLoadState("networkidle");
		}
		await page.getByLabel("Name", { exact: true }).fill("Cat Facts");
		const renameResponsePromise = page.waitForResponse((response) =>
			response.request().method() === "PUT" && new URL(response.url()).pathname === labelPath,
		);
		await page.getByRole("button", { name: "Rename" }).click();
		const renameResponse = await renameResponsePromise;
		const renameHeaders = renameResponse.headers();
		assert(renameResponse.status() === 200,
			`rename returned ${renameResponse.status()}`);
		assert(renameHeaders["hx-redirect"] === labelPath,
			`rename HX-Redirect was ${renameHeaders["hx-redirect"]}`);
		assert(isExplicitlyEmptyResponse(renameHeaders),
			`rename response was not explicitly empty: ${JSON.stringify(renameHeaders)}`);
		const settledRename = await waitForSettledRename(page, "Cat Facts");
		assert(settledRename.headingVisible,
			"renamed label heading did not settle");
		assert(settledRename.inputValue === "Cat Facts",
			"rename form did not settle with the updated name");
		const desktopLabel = await checkPage(page, "label-detail-desktop");
		await screenshot(page, "label-detail-desktop.png");
		await page.goto(`${baseURL}/admin/agents`, { waitUntil: "networkidle" });
		assert((await page.locator("body").innerText()).includes("Cat Facts"), "agent list lacks label badges");
		const desktopAgentBadges = await checkPage(page, "agent-label-badges-desktop");
		await screenshot(page, "agent-label-badges-desktop.png");

		await page.setViewportSize({ width: 375, height: 812 });
		await page.goto(`${baseURL}${labelPath}`, { waitUntil: "networkidle" });
		const mobileLabel = await checkPage(page, "label-detail-mobile");
		await screenshot(page, "label-detail-mobile.png");
		await page.goto(`${baseURL}/admin/agents`, { waitUntil: "networkidle" });
		const mobileAgentBadges = await checkPage(page, "agent-label-badges-mobile");
		assert((await page.locator("body").innerText()).includes("Cat Facts"),
			"mobile agent list lacks label badges");
		await screenshot(page, "agent-label-badges-mobile.png");

		const csrf = await page.locator('meta[name="csrf-token"]').getAttribute("content");
		const api = async (method, path, body) => page.evaluate(async ({ method, path, body, csrf }) => {
			const response = await fetch(path, {
				method,
				headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf },
				body: body ? JSON.stringify(body) : undefined,
			});
			return { status: response.status, body: await response.text() };
		}, { method, path, body, csrf });
		const expectAPIError = async (method, path, status, body) => {
			const response = await api(method, path, body);
			assert(response.status === status, `${method} ${path} returned ${response.status}`);
			expectedHTTPFailures.push({ method, path, status });
			return response;
		};
		const duplicate = await expectAPIError(
			"POST", "/admin/agent-labels", 409, { name: "CAT FACTS" },
		);
		const unpaired = await expectAPIError(
			"POST", `${labelPath}/members`, 409, { agent_id: "label-unpaired" },
		);

		await run("sqlite3", [database, `
INSERT INTO projects (name) VALUES ('Label reference project');
INSERT INTO project_execution_policies (project_id, target_mode, agent_label_id, agent_strategy)
VALUES (last_insert_rowid(), 'label', ${labelID}, 'all');`]);
		const referencedDelete = await expectAPIError("DELETE", labelPath, 409);

		await page.goto(`${baseURL}/admin/agents/label-agent-1`, { waitUntil: "networkidle" });
		const deletionNavigation = page.waitForURL(`${baseURL}/admin/agents`);
		const deletionDialog = page.waitForEvent("dialog");
		const deletionClick = page.getByRole(
			"button", { name: "Permanently delete agent" },
		).click();
		const dialog = await deletionDialog;
		assert(dialog.type() === "confirm", `agent deletion opened ${dialog.type()} dialog`);
		await dialog.accept();
		await Promise.all([deletionClick, deletionNavigation]);
		const deletionSummary = await runOutput("sqlite3", [database,
			`SELECT COUNT(*) FROM agent_label_memberships WHERE agent_id = 'label-agent-1';`]);
		assert(deletionSummary.trim() === "0", "agent deletion retained label membership");

		const roleResults = {};
		for (const [role, credentials] of Object.entries({ deployer, viewer })) {
			const roleContext = await browser.newContext();
			const rolePage = await roleContext.newPage();
			await rolePage.goto(`${baseURL}/login`);
			await rolePage.getByLabel("Email").fill(credentials.email);
			await rolePage.getByLabel("Password").fill(credentials.password);
			await rolePage.getByRole("button", { name: "Login" }).click();
			const response = await rolePage.goto(`${baseURL}/admin/agent-labels`);
			roleResults[role] = response.status();
			await roleContext.close();
		}
		assert(roleResults.deployer === 403 && roleResults.viewer === 403,
			`role denial statuses: ${JSON.stringify(roleResults)}`);
		const unexpectedConsoleErrors = findUnexpectedConsoleErrors(
			consoleErrors, expectedHTTPFailures, baseURL,
		);
		assert(unexpectedConsoleErrors.length === 0,
			`browser console errors: ${consoleErrorTexts(unexpectedConsoleErrors).join("; ")}`);
		await saveJSON("label-scenario.json", {
			labelID, memberCount: 3, duplicate, unpaired, referencedDelete,
			roleResults, deletionMembershipCount: deletionSummary.trim(),
			rename: {
				status: renameResponse.status(), hxRedirect: renameHeaders["hx-redirect"],
				contentLength: renameHeaders["content-length"],
			},
		});
		await saveJSON("viewport-metadata.json", {
			desktop: {
				width: 1280, height: 768, label: desktopLabel,
				agentBadges: desktopAgentBadges,
			},
			mobile: {
				width: 375, height: 812, label: mobileLabel,
				agentBadges: mobileAgentBadges,
			},
		});
		await saveJSON("browser-console.json", { errors: consoleErrorTexts(unexpectedConsoleErrors) });
	} else {

	await page.setViewportSize({ width: 1280, height: 768 });
	await page.goto(`${baseURL}/admin/agents`, { waitUntil: "networkidle" });
	const desktopAgents = await checkPage(page, "agents-desktop");
	await screenshot(page, "agents-desktop.png");
	await page.goto(`${baseURL}/admin/agents/new`, { waitUntil: "networkidle" });
	const newAgentFocus = await tabOrder(page);
	assert(await page.getByLabel("Agent fingerprint").count() === 0, "new agent form asks for a fingerprint");
	await screenshot(page, "agent-new-desktop.png");
	await page.setViewportSize({ width: 768, height: 1024 });
	await screenshot(page, "agent-new-tablet.png");
	await page.setViewportSize({ width: 375, height: 812 });
	await screenshot(page, "agent-new-mobile.png");
	await page.setViewportSize({ width: 1280, height: 768 });
	await page.getByLabel("Name").fill("Browser Agent");
	await page.getByLabel("Agent host or IP address").fill("127.0.0.1");
	await page.getByLabel(/Port/).fill(new URL(bootstrapURL).port);
	await page.getByLabel("Pairing code").fill(pairing.code);
	await page.getByRole("button", { name: "Create agent" }).click();
	await page.getByLabel("Type the agent fingerprint to confirm").waitFor();
	await screenshot(page, "agent-confirm-desktop.png");
	await page.setViewportSize({ width: 768, height: 1024 });
	await screenshot(page, "agent-confirm-tablet.png");
	await page.setViewportSize({ width: 375, height: 812 });
	await screenshot(page, "agent-confirm-mobile.png");
	await page.setViewportSize({ width: 1280, height: 768 });
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
	assert(consoleErrors.length === 0,
		`browser console errors: ${consoleErrorTexts(consoleErrors).join("; ")}`);
	await saveJSON("viewport-metadata.json", {
		desktop: { width: 1280, height: 768, agents: desktopAgents, assignment: assignmentDesktop },
		mobile: { width: 375, height: 812, agents: mobileAgents, assignment: assignmentMobile },
	});
	await saveJSON("keyboard-navigation.json", { newAgentFocus });
	await saveJSON("listener-runtime.json", { agentPaired: true, pollObserved: true, publicURL: "configured" });
	await saveJSON("database-summary.json", { pairedDirectAssignment: databaseSummary.trim() });
	await saveJSON("browser-console.json", { errors: consoleErrorTexts(consoleErrors) });
	}
	} catch (error) {
		const diagnostics = {
			error: redactDiagnostic(error instanceof Error ? error.message : String(error)),
			consoleErrors: consoleErrorTexts(consoleErrors).map(redactDiagnostic),
		};
		await fs.mkdir(outputDir, { recursive: true });
		await saveJSON("browser-failure.json", diagnostics);
		console.error("browser proof diagnostics:", JSON.stringify(diagnostics));
		throw error;
	} finally {
		if (addressReservations) await addressReservations.release();
		if (browser) await browser.close();
	if (agent?.exitCode === null) {
		const exited = new Promise((resolve) => agent.once("exit", resolve));
		agent.kill("SIGTERM");
		await exited;
		receipt.agentStopped = true;
	}
	if (!agent || agent.exitCode !== null) receipt.agentStopped = true;
	if (server?.exitCode === null) {
		const exited = new Promise((resolve) => server.once("exit", resolve));
		server.kill("SIGTERM");
		await exited;
		receipt.serverStopped = true;
	}
	if (!server || server.exitCode !== null) receipt.serverStopped = true;
	await fs.rm(serverDir, { recursive: true, force: true });
	receipt.serverDirectoryRemoved = true;
	await fs.mkdir(outputDir, { recursive: true });
	await saveJSON("cleanup.json", receipt);
}
