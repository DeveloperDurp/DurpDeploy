import assert from "node:assert/strict";
import { join } from "node:path";
import { createRoutingHarness, until } from "./agent_routing_harness.mjs";

export async function runFullRoutingProof(outputDir) {
	const h = await createRoutingHarness(outputDir);
	const { page, api, rows, sql, save, base, agents } = h;
	const scenarios = {};
	try {
		await page.goto(`${base}/admin/agent-labels`);
		await page.getByLabel("Name", { exact: true }).fill("Cat Fact");
		await page.getByRole("button", { name: "Create label" }).click();
		await page.waitForURL(/\/admin\/agent-labels\/\d+$/);
		const labelID = Number(new URL(page.url()).pathname.split("/").at(-1));
		for (const agent of agents) {
			await page.getByLabel("Active paired agent").selectOption(agent);
			await page.getByRole("button", { name: "Add member" }).click();
			await page.waitForLoadState("networkidle");
		}
		const environment = await api("POST", "/environments", { name: "Cat environment" }, 201);
		const project = await api("POST", "/projects", { name: "Cat Facts", target_mode: "label", agent_label_id: labelID, agent_strategy: "round_robin" }, 201);
		const projectPath = `/projects/${project.id}`;
		const marker = `cat-fact-${project.id}`;
		const step = await api("POST", `${projectPath}/steps`, { name: "Cat Fact", script_body: `echo '${marker}: cats have whiskers'`, sort_order: 0, timeout_seconds: 30 }, 201);
		const release = await api("POST", `${projectPath}/releases`, { version: "cat-1" }, 201);
		const request = { release_id: release.id, environment_id: environment.id };
		const deploy = (override = {}) => api("POST", `${projectPath}/deployments`, { ...request, ...override }, 201);
		const targets = (id) => rows(`SELECT agent_id FROM deployment_routing_agents WHERE deployment_id=${id} ORDER BY position;`).then((list) => list.map((row) => row.agent_id));
		const children = (id) => rows(`SELECT id,status,target_agent_id FROM deployments WHERE parent_deployment_id=${id} ORDER BY id;`);
		const status = (id) => rows(`SELECT status FROM deployments WHERE id=${id};`).then((list) => list[0].status);
		async function settled(id, expected = "succeeded") {
			await until(async () => {
				const value = await status(id);
				if (["succeeded", "failed", "cancelled"].includes(value)) {
					await save(`settled-${id}.json`, { status: value, logs: await rows(`SELECT deployment_id,sequence,line FROM deployment_logs WHERE deployment_id=${id} OR deployment_id IN (SELECT id FROM deployments WHERE parent_deployment_id=${id}) ORDER BY deployment_id,sequence;`) });
					assert.equal(value, expected, `deployment ${id}`); return true;
				}
				return false;
			}, `deployment ${id} settles ${expected}`);
			const executions = await children(id);
			for (const child of executions.length ? executions : [{ id }]) {
				const logs = await rows(`SELECT sequence,line FROM deployment_logs WHERE deployment_id=${child.id} ORDER BY sequence;`);
				assert(logs.some((log) => log.line.includes(marker)), `real output absent for ${child.id}`);
				await save(`execution-${child.id}.json`, { id: child.id, status: await status(child.id), logs });
			}
		}
		await page.goto(`${base}${projectPath}/deploy?release_id=${release.id}`);
		await page.getByLabel("Environment", { exact: true }).selectOption(String(environment.id));
		await page.getByLabel("Execution target", { exact: true }).selectOption("local");
		await page.screenshot({ path: join(outputDir, "local-override-form.png"), fullPage: true });
		await page.getByRole("button", { name: "Deploy", exact: true }).click();
		await page.waitForURL(/\/deployments\/\d+$/);
		const local = { id: Number(new URL(page.url()).pathname.split("/").at(-1)) };
		await settled(local.id);
		assert.deepEqual(await targets(local.id), []);
		scenarios.local = { id: local.id, status: await status(local.id) };
		const sequential = [];
		for (let i = 0; i < 3; i++) { const d = await deploy(); await settled(d.id); sequential.push({ id: d.id, targets: await targets(d.id) }); }
		assert.deepEqual(sequential.flatMap((d) => d.targets), [...agents].sort());
		scenarios.roundRobinSequential = sequential;
		const concurrent = await Promise.all(Array.from({ length: 3 }, () => deploy()));
		await Promise.all(concurrent.map((d) => settled(d.id)));
		const concurrentTargets = await Promise.all(concurrent.map((d) => targets(d.id)));
		assert.deepEqual(concurrentTargets.flat().sort(), [...agents].sort());
		scenarios.roundRobinConcurrent = concurrent.map((d, index) => ({ id: d.id, targets: concurrentTargets[index] }));
		const allInput = { target_mode: "label", agent_label_id: labelID, agent_strategy: "all" };
		const all = await deploy(allInput);
		await settled(all.id);
		const allChildren = await children(all.id);
		assert.equal(allChildren.length, 3);
		assert.deepEqual(allChildren.map((d) => d.target_agent_id).sort(), [...agents].sort());
		assert.equal((await rows(`SELECT * FROM deployment_dispatches WHERE deployment_id=${all.id};`)).length, 0);
		scenarios.fanout = { id: all.id, children: allChildren };
		for (const [name, width, height] of [["desktop", 1280, 900], ["tablet", 768, 1024], ["mobile", 375, 812]]) {
			await page.setViewportSize({ width, height });
			await page.goto(`${base}/deployments/${all.id}`);
			await page.locator("[data-fanout-children]").waitFor();
			assert.equal(await page.locator("#log-container").count(), 0);
			assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, `${name} overflow`);
			await page.screenshot({ path: join(outputDir, `parent-${name}.png`), fullPage: true });
			for (const child of allChildren) {
				await page.locator(`[data-child-log="${child.id}"]:visible`).click();
				await page.waitForURL(`${base}/deployments/${child.id}#logs`);
				assert((await page.locator("#logs").innerText()).includes(marker));
				await page.screenshot({ path: join(outputDir, `child-${child.id}-${name}.png`), fullPage: true });
				await page.goto(`${base}/deployments/${all.id}`);
			}
		}
		await api("GET", `/deployments/${all.id}/logs`, undefined, 409);
		for (const child of allChildren) for (const action of ["retry", "redeploy", "cancel"]) await api("POST", `/deployments/${child.id}/${action}`, undefined, 409);
		const beforeInvalid = await rows("SELECT count(*) AS n FROM deployments;");
		await api("POST", `${projectPath}/deployments`, { ...request, target_mode: "malformed" }, 422);
		assert.deepEqual(await rows("SELECT count(*) AS n FROM deployments;"), beforeInvalid);
		scenarios.invalidRollback = true;
		await api("PUT", `${projectPath}/steps/${step.id}`, { name: "Cat Fact", script_body: `echo '${marker}: intentional failure'; exit 1`, sort_order: 0, timeout_seconds: 30 }, 200);
		const failedRelease = await api("POST", `${projectPath}/releases`, { version: "cat-failure" }, 201);
		const failed = await deploy({ ...allInput, release_id: failedRelease.id });
		await settled(failed.id, "failed");
		await api("DELETE", `/admin/agent-labels/${labelID}/members/${agents[2]}`, undefined, 204);
		const retry = await api("POST", `/deployments/${failed.id}/retry`, undefined, 201);
		await settled(retry.id, "failed");
		assert.deepEqual(await targets(retry.id), await targets(failed.id));
		const redeploy = await api("POST", `/deployments/${failed.id}/redeploy`, undefined, 201);
		await settled(redeploy.id, "failed");
		assert.equal((await targets(redeploy.id)).length, 1);
		assert(!(await targets(redeploy.id)).includes(agents[2]));
		scenarios.retryRedeploy = { retry: retry.id, retryTargets: await targets(retry.id), redeploy: redeploy.id, redeployTargets: await targets(redeploy.id) };
		await api("POST", `/admin/agent-labels/${labelID}/members`, { agent_id: agents[2] }, 201);
		await sql(`INSERT INTO lifecycles(id,name) VALUES(100,'Approval proof'); INSERT INTO lifecycle_stages(lifecycle_id,environment_id,sort_order,requires_approval) VALUES(100,${environment.id},0,1); UPDATE projects SET lifecycle_id=100 WHERE id=${project.id};`);
		const pending = await deploy(allInput);
		assert.equal(await status(pending.id), "pending_approval");
		assert.deepEqual(await children(pending.id), []);
		for (const agent of agents) await api("DELETE", `/admin/agent-labels/${labelID}/members/${agent}`, undefined, 204);
		const pendingBefore = await rows(`SELECT * FROM deployments WHERE id=${pending.id};`);
		await api("POST", `/deployments/${pending.id}/approve`, undefined, 409);
		assert.deepEqual(await rows(`SELECT * FROM deployments WHERE id=${pending.id};`), pendingBefore);
		assert.deepEqual(await children(pending.id), []);
		for (const agent of agents) await api("POST", `/admin/agent-labels/${labelID}/members`, { agent_id: agent }, 201);
		const approvals = await Promise.all(Array.from({ length: 2 }, () => api("POST", `/deployments/${pending.id}/approve`, undefined, [200, 409])));
		assert.deepEqual(approvals.map((response) => response.status).sort(), [200, 409]);
		await settled(pending.id);
		assert.equal((await children(pending.id)).length, 3);
		scenarios.approval = { id: pending.id, winners: approvals.length - 1, unavailableRollback: true };
		await sql(`UPDATE projects SET lifecycle_id=NULL WHERE id=${project.id};`);
		const emptyLabel = await api("POST", "/admin/agent-labels", { name: "Empty" }, 201);
		const noMatchBefore = await rows("SELECT count(*) AS n FROM deployments;");
		await api("POST", `${projectPath}/deployments`, { ...request, ...allInput, agent_label_id: emptyLabel.id }, 422);
		assert.deepEqual(await rows("SELECT count(*) AS n FROM deployments;"), noMatchBefore);
		scenarios.noMatchRollback = true;
		const schedule = await api("POST", `${projectPath}/schedules`, { ...request, cron: "0 * * * *", enabled: true, target_mode: "inherit" }, 201);
		await sql(`UPDATE scheduled_deployments SET next_run_at=unixepoch()-1 WHERE id=${schedule.id};`);
		const occurrence = await until(async () => (await rows(`SELECT deployment_id FROM scheduled_deployment_occurrences WHERE scheduled_deployment_id=${schedule.id};`))[0], "scheduled occurrence", 75000);
		await settled(occurrence.deployment_id);
		assert.equal((await rows(`SELECT * FROM scheduled_deployment_occurrences WHERE scheduled_deployment_id=${schedule.id};`)).length, 1);
		scenarios.schedule = { id: schedule.id, deployment: occurrence.deployment_id };
		await api("PUT", `${projectPath}/steps/${step.id}`, { name: "Cat Fact", script_body: `for i in {1..120}; do echo '${marker}: waiting'; done; exec sleep 45`, sort_order: 0, timeout_seconds: 60 }, 200);
		const slowRelease = await api("POST", `${projectPath}/releases`, { version: "cat-slow" }, 201);
		const cancel = await deploy({ ...allInput, release_id: slowRelease.id });
		await until(async () => (await children(cancel.id)).length === 3 && (await children(cancel.id)).every((child) => child.status === "running"), "all children started");
		await until(async () => (await rows(`SELECT deployment_id FROM deployment_logs WHERE deployment_id IN (SELECT id FROM deployments WHERE parent_deployment_id=${cancel.id}) GROUP BY deployment_id HAVING count(*)>=100;`)).length === 3, "all real scripts produced output before cancellation");
		await api("POST", `/deployments/${cancel.id}/cancel`);
		await api("POST", `/deployments/${cancel.id}/cancel`);
		await settled(cancel.id, "cancelled");
		scenarios.cancel = { id: cancel.id, children: await children(cancel.id) };
		const history = await rows("SELECT id,status,parent_deployment_id,target_agent_id FROM deployments ORDER BY id;");
		await h.restart();
		await h.restart();
		assert.deepEqual(await rows("SELECT id,status,parent_deployment_id,target_agent_id FROM deployments ORDER BY id;"), history);
		await page.goto(`${base}/deployments/${all.id}`);
		await page.locator("[data-fanout-children]").waitFor();
		await page.screenshot({ path: join(outputDir, "restart-history.png"), fullPage: true });
		scenarios.restart = { unchanged: true, deploymentCount: history.length };
		await save("database.json", history);
		await save("browser-console.json", { errors: h.errors });
		assert.deepEqual(h.errors, []);
		await save("result.json", { passed: true, realAgentCount: agents.length, scenarios });
		console.log("full-routing real three-agent execution: PASS");
	} finally { await h.cleanup(); }
}
