import assert from "node:assert/strict";
import { createHash, randomBytes } from "node:crypto";
import { promises as fs } from "node:fs";
import { execFile, spawn } from "node:child_process";
import { promisify } from "node:util";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { chromium } from "playwright";
import { reserveHarnessAddresses } from "./agent_admin_browser_proof_support.mjs";

const exec = promisify(execFile);

export async function runFanoutProof(outputDir) {
	await fs.mkdir(outputDir, { recursive: true });
	const temp = await fs.mkdtemp(join(tmpdir(), "durpdeploy-fanout-"));
	const binary = join(temp, "server");
	const database = join(temp, "proof.db");
	const reservation = await reserveHarnessAddresses([undefined]);
	const address = reservation.addresses[0];
	const base = `http://${address}`;
	const tokenPart = randomBytes(32).toString("hex");
	const token = `ddp_pat_${tokenPart}`;
	const password = randomBytes(24).toString("hex");
	const env = { ...process.env, DURPDEPLOY_ADDR: address, DURPDEPLOY_DB: `${database}?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)`, DURPDEPLOY_SECRET_KEY: randomBytes(32).toString("base64"), DURPDEPLOY_AGENT_IDENTITY_DIR: "", DURPDEPLOY_AGENT_LISTEN_ADDR: "", DURPDEPLOY_AGENT_PUBLIC_URL: "", DURPDEPLOY_AGENT_PENDING_FINGERPRINT: "" };
	let server;
	let browser;
	const errors = [];
	let serverOutput = "";
	const save = async (name, value) => fs.writeFile(join(outputDir, name), typeof value === "string" ? value : JSON.stringify(value, null, 2));
	const sql = async (query) => (await exec("sqlite3", [database, query])).stdout;
	const credentials = [];
	try {
		await save("resources.json", { temp, database, address, ownsCache: false });
		await exec("go", ["build", "-buildvcs=false", "-o", binary, "./cmd/server"], { env, timeout: 120000 });
		for (const role of ["admin", "viewer"]) {
			await exec(binary, ["admin", "create", "--email", `${role}@fanout.test`, "--password", password], { env });
		}
		await sql(`UPDATE users SET role='viewer' WHERE email='viewer@fanout.test';
INSERT INTO api_tokens(id,user_id,name,token_prefix,token_hash) SELECT 'proof',id,'proof','ddp_pat_test','${createHash("sha256").update(tokenPart).digest("hex")}' FROM users WHERE role='admin';`);
		await reservation.release();
		server = spawn(binary, [], { env, stdio: ["ignore", "pipe", "pipe"] });
		server.stdout.on("data", (data) => { serverOutput += data; });
		server.stderr.on("data", (data) => { serverOutput += data; });
		let ready = false;
		for (let attempt = 0; attempt < 100; attempt++) {
			try { if ((await fetch(`${base}/login`)).ok) { ready = true; break; } }
			catch (error) { if (error.cause?.code !== "ECONNREFUSED") throw error; }
			await new Promise((resolve) => setTimeout(resolve, 100));
		}
		assert(ready,"temporary application did not become ready");
		await sql(`BEGIN;
INSERT INTO projects(id,name) VALUES(1,'Fan-out proof');
INSERT INTO project_members(project_id,user_id,role) SELECT 1,id,'deployer' FROM users WHERE role='viewer';
INSERT INTO environments(id,name) VALUES(1,'Proof environment');
INSERT INTO releases(id,project_id,version,steps_json) VALUES(1,1,'v1','[]');
INSERT INTO agent_labels(id,name,normalized_name) VALUES(1,'Frozen label','frozen label');
INSERT INTO deployments(id,release_id,environment_id,status,started_at) VALUES(1,1,1,'running',unixepoch());
INSERT INTO deployment_routing_snapshots(deployment_id,source,target_mode,agent_label_id,agent_label_name,agent_strategy) VALUES(1,'request','label',1,'Frozen label','all');
${[2,3,4].map((id, i) => `INSERT INTO agents(id,name,status,certificate_pem,certificate_fingerprint,last_heartbeat_at) VALUES('agent-${id}','Current ${id}','active','fixture','${String(id).repeat(64)}',unixepoch());
INSERT INTO deployment_routing_agents(deployment_id,position,agent_id,agent_name) VALUES(1,${i},'agent-${id}','Copied Agent ${id}');
INSERT INTO deployments(id,release_id,environment_id,status,parent_deployment_id,target_agent_id,target_agent_name,started_at) VALUES(${id},1,1,'${id === 2 ? "succeeded" : id === 3 ? "running" : "failed"}',1,'agent-${id}','Copied Agent ${id}',unixepoch());
INSERT INTO deployment_dispatches(deployment_id,mode,state,assigned_agent_id,agent_id,reason) VALUES(${id},'remote','${id === 2 ? "succeeded" : id === 3 ? "started" : "failed"}','agent-${id}','agent-${id}','${id === 4 ? "target unavailable" : ""}');
INSERT INTO deployment_logs(deployment_id,sequence,line) VALUES(${id},1,'child-${id}-log');`).join("\n")}
COMMIT;`);
		browser = await chromium.launch({ headless: true });
		const context = await browser.newContext();
		const page = await context.newPage();
		page.on("pageerror", (error) => errors.push(error.message));
		page.on("console", (message) => { if (message.type() === "error") errors.push(message.text()); });
		await page.goto(`${base}/login`);
		await page.getByLabel("Email").fill("admin@fanout.test");
		await page.getByLabel("Password").fill(password);
		await page.getByRole("button", { name: "Login", exact: true }).click();
		await page.waitForURL(`${base}/`);
		const cookies = (await context.cookies()).map((cookie) => `${cookie.name}=${cookie.value}`).join("; ");
		credentials.push(cookies, token, password);
		const views = [];
		for (const [name, width, height] of [["desktop",1280,900],["tablet",768,1024],["mobile",375,812]]) {
			await page.setViewportSize({ width, height });
			await page.goto(`${base}/deployments/1`);
			await page.locator("[data-fanout-children]").waitFor();
			assert.equal(await page.locator("#log-container").count(), 0);
			for (const id of [2,3,4]) assert((await page.locator("body").innerText()).includes(`Copied Agent ${id}`));
			const overflow = await page.evaluate(() => document.documentElement.scrollWidth > innerWidth);
			assert.equal(overflow, false, `${name} overflow`);
			const clipped = await page.locator('[data-fanout-children] a:visible').evaluateAll((links) => links.filter((link) => {
				const box = link.getBoundingClientRect();
				const container = link.closest('td, article').getBoundingClientRect();
				return box.left < 0 || box.right > innerWidth || box.left < container.left || box.right > container.right;
			}).map((link) => link.textContent));
			assert.deepEqual(clipped,[],`${name} clipped links`);
			await page.screenshot({ path: join(outputDir, `parent-${name}.png`), fullPage: true });
			for (const id of [2,3,4]) {
				await page.locator(`[data-child-log="${id}"]:visible`).click();
				await page.waitForURL(`${base}/deployments/${id}#logs`);
				assert((await page.locator("#logs").innerText()).includes(`child-${id}-log`));
				assert.equal(await page.locator('[hx-post]').count(), 0);
				await page.screenshot({ path: join(outputDir, `child-${id}-${name}.png`), fullPage: true });
				await page.goto(`${base}/deployments/1`);
			}
			views.push({ name, width, height, childIDs: [2,3,4], overflow, clipped });
		}
		const probe = async (name, path, isAPI, stream = false) => {
			let raw;
			try { raw = (await exec("curl", ["-sS", "-i", "--max-time", stream ? "1" : "5", "-H", isAPI ? `Authorization: Bearer ${token}` : `Cookie: ${cookies}`, `${base}${path}`])).stdout; }
			catch (error) { if (stream && error.code === 28) raw = error.stdout; else throw new Error(`${name} request failed`); }
			for (const value of credentials) assert(!raw.includes(value), "credential in response");
			await save(`${name}.http`, raw);
			return raw;
		};
		for (const [name,path,isAPI] of [
			["parent-html-sse","/deployments/1/logs/stream",false], ["parent-html-text","/deployments/1/logs.txt",false],
			["parent-json","/api/v1/deployments/1/logs",true], ["parent-single","/api/v1/deployments/1/logs/1",true],
			["parent-sse","/api/v1/deployments/1/logs/stream",true], ["parent-ndjson","/api/v1/deployments/1/logs/stream?format=ndjson",true], ["parent-text","/api/v1/deployments/1/logs.txt",true],
			["parent-malformed-format","/api/v1/deployments/1/logs/stream?format=bad",true], ["parent-malformed-log","/api/v1/deployments/1/logs/bad",true],
		]) {
			const raw = await probe(name,path,isAPI);
			assert.match(raw, /^HTTP\/1.1 409 /);
			assert.match(raw, isAPI ? /content-type: application\/json\r\n/i : /content-type: text\/plain; charset=utf-8\r\n/i);
			const body = raw.split("\r\n\r\n")[1];
			if (isAPI) { const data=JSON.parse(body); assert.equal(data.code,"fanout_parent_has_no_logs"); assert.deepEqual(data.children.map((child)=>child.id),[2,3,4]); }
			else assert.equal(body,"Fan-out parents have no logs. Open a child deployment to view its logs.\n");
		}
		for (const id of [2,3,4]) for (const [format,suffix] of [["text","logs.txt"],["sse","logs/stream"],["ndjson","logs/stream?format=ndjson"]]) {
			const raw = await probe(`child-${id}-${format}`, `/api/v1/deployments/${id}/${suffix}`,true,format!=="text");
			assert.match(raw,/^HTTP\/1.1 200 /); assert(raw.includes(`child-${id}-log`));
		}
		for (const id of [2,3,4]) for (const action of ["cancel","retry","redeploy"]) {
			const raw = (await exec("curl",["-sS","-i","--max-time","5","-X","POST","-H",`Authorization: Bearer ${token}`,`${base}/api/v1/deployments/${id}/${action}`])).stdout;
			await save(`child-${id}-${action}.http`,raw);
			assert.match(raw,/^HTTP\/1.1 409 /);
		}
		const response = await probe("parent-detail","/api/v1/deployments/1",true);
		const detail = JSON.parse(response.split("\r\n\r\n")[1]);
		assert.deepEqual(detail.dispatch.children.map((child)=>[child.id,child.status]),[[2,"succeeded"],[3,"running"],[4,"failed"]]);
		await save("database.json", JSON.parse(await sql("SELECT json_group_array(json_object('id',id,'parent',parent_deployment_id,'status',status,'agent',target_agent_name)) FROM deployments;")));
		for (const path of ["/", "/deployments", "/projects/1/releases/1"]) {
			await page.goto(`${base}${path}`);
			for (const id of [2,3,4]) assert.equal(await page.locator(`a[href="/deployments/${id}"]`).count(),0);
		}
		await sql(`INSERT INTO deployments(id,release_id,environment_id,status) VALUES(5,1,1,'failed');
INSERT INTO scheduled_deployments(id,project_id,release_id,environment_id,cron,next_run_at,enabled) VALUES(1,1,1,1,'0 * * * *',2000000000,0);
INSERT INTO scheduled_deployment_occurrences(scheduled_deployment_id,due_at,deployment_id,routing_source,target_mode,agent_label_id,agent_label_name,agent_strategy) VALUES(1,1,5,'schedule','label',1,'Frozen label','all');`);
		for (const [name,width,height] of [["desktop",1280,900],["tablet",768,1024],["mobile",375,812]]) {
			await page.setViewportSize({width,height});
			assert.equal((await page.goto(`${base}/deployments/5`)).status(),200);
			assert.equal(await page.locator('#log-container').count(),0);
			assert(!(await page.locator('[data-fanout-children]').innerText()).includes('approved'));
			await page.screenshot({path:join(outputDir,`empty-parent-${name}.png`),fullPage:true});
		}
		const emptyRaw = await probe("empty-parent-conflict","/api/v1/deployments/5/logs",true);
		assert.match(emptyRaw,/^HTTP\/1.1 409 /);
		assert.deepEqual(JSON.parse(emptyRaw.split("\r\n\r\n")[1]).children,[]);
		const viewer = await browser.newContext();
		const viewerPage = await viewer.newPage();
		await viewerPage.goto(`${base}/login`);
		await viewerPage.getByLabel("Email").fill("viewer@fanout.test");
		await viewerPage.getByLabel("Password").fill(password);
		await viewerPage.getByRole("button", { name: "Login", exact: true }).click();
		await viewerPage.waitForURL(`${base}/`);
		const viewerResponse = await viewerPage.goto(`${base}/deployments/1`);
		assert.equal(viewerResponse.status(),200);
		assert.equal(await viewerPage.locator('[data-fanout-children]').count(),1);
		assert.equal(await viewerPage.locator('[hx-post]').count(),0);
		await viewerPage.screenshot({path:join(outputDir,"viewer-parent.png"),fullPage:true});
		await viewer.close();
		const csrf = await page.locator('meta[name="csrf-token"]').getAttribute("content");
		credentials.push(csrf);
		const deletion = (await exec("curl",["-sS","-i","--max-time","5","-X","DELETE","-H",`Cookie: ${cookies}`,"-H",`X-CSRF-Token: ${csrf}`,`${base}/admin/agents/agent-4`])).stdout;
		await save("delete-agent.http",deletion);
		assert.match(deletion,/^HTTP\/1.1 204 /);
		await page.goto(`${base}/deployments/1`);
		assert((await page.locator("[data-fanout-children]").innerText()).includes("agent-4 · deleted"));
		assert((await page.locator("[data-fanout-children]").innerText()).includes("Copied Agent 4"));
		await page.screenshot({path:join(outputDir,"parent-deleted-agent.png"),fullPage:true});
		for (const attempt of [1,2]) {
			const raw = (await exec("curl",["-sS","-i","--max-time","5","-X","POST","-H",`Authorization: Bearer ${token}`,`${base}/api/v1/deployments/1/cancel`])).stdout;
			await save(`parent-cancel-${attempt}.http`,raw);
			assert.match(raw,/^HTTP\/1.1 200 /);
			const response=JSON.parse(raw.split("\r\n\r\n")[1]);
			assert.equal(response.status,(await sql("SELECT status FROM deployments WHERE id=1;")).trim());
			assert.equal((await sql("SELECT state FROM deployment_dispatches WHERE deployment_id=3;")).trim(),"cancel_requested");
		}
		assert.deepEqual(errors,[]);
		await save("browser-console.json",{errors});
		await save("viewport-metadata.json",views);
		await save("result.json",{passed:true,parentID:1,childIDs:[2,3,4],parentFormats:10,childFormats:9,childActionConflicts:9,parentCancellationRequests:2,viewerStatus:200,deletedIdentityPreserved:true,emptyParentViewports:3});
	} finally {
		if (browser) await browser.close();
		if (server && server.exitCode === null) {
			const exited = new Promise((resolve)=>server.once("exit",resolve));
			server.kill("SIGTERM"); await exited;
		}
		await reservation.release();
		await fs.rm(temp,{recursive:true,force:true});
		for (const value of [token, password, env.DURPDEPLOY_SECRET_KEY, ...credentials]) serverOutput = serverOutput.replaceAll(value, "[redacted]");
		await save("server.log",serverOutput);
		await save("cleanup.json",{serverStopped:!server || server.exitCode!==null,browserClosed:true,tempRemoved:true,address});
	}
}
