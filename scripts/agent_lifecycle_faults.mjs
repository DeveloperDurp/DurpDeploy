import { join } from "node:path";

import { createFaultClient } from "./agent_fault_client.mjs";

const lifecycleScenarios = new Set([
	"wrong-token", "duplicate-poll", "pre-start-loss", "post-start-loss",
	"cancel-unconfirmed", "lost-poll-response", "lost-start-response",
	"lost-heartbeat-response", "lost-logs-response", "lost-result-response",
	"lost-cancelled-response",
]);

function check(condition, message) {
	if (!condition) throw new Error(message);
}

async function waitFor(readValue, accept, description, attempts = 1800) {
	let lastValue = "";
	for (let attempt = 0; attempt < attempts; attempt += 1) {
		const value = (await readValue()).trim();
		lastValue = value;
		if (accept(value)) return value;
		await new Promise((resolve) => setTimeout(resolve, 50));
	}
	throw new Error(`timed out waiting for ${description}; last=${lastValue}`);
}

export function isLifecycleScenario(scenario) {
	return lifecycleScenarios.has(scenario);
}

export async function runLifecycleFault(context) {
	const {
		agentStateDir, api, command, environmentID, lifecycleAddress,
		lifecycleProxy, readOnly, restartAgent, scenario, serverIdentity, stopAgent,
	} = context;
	check(isLifecycleScenario(scenario), `unknown lifecycle fault scenario: ${scenario}`);
	const createDeployment = async (scriptBody) => {
		const project = await api("POST", "/projects", { name: `Fault ${scenario}` });
		await api("POST", `/projects/${project.id}/steps`, {
			agent_selectors: ["linux"],
			execution_target: "agent",
			max_retries: 0,
			name: "fault-step",
			script_body: scriptBody,
			sort_order: 1,
			timeout_seconds: 120,
		});
		const release = await api("POST", `/projects/${project.id}/releases`, {
			version: `fault-${scenario}`,
		});
		return api("POST", `/projects/${project.id}/deployments`, {
			environment_id: Number(environmentID),
			release_id: release.id,
		});
	};
	const claim = (id) => readOnly(
		`SELECT state||'||'||` +
		`COALESCE(hex(claim_token_hash),'')||'|'||COALESCE(started_at,0)||'|'||` +
		`COALESCE(last_heartbeat_at,0) FROM remote_step_runs ` +
		`WHERE deployment_id=${Number(id)} ` +
		`ORDER BY step_index, agent_id LIMIT 1;`,
	);
	const deploymentStatus = (id) => readOnly(
		`SELECT d.status||'|'||r.state||'|' ` +
		`FROM deployments d JOIN remote_step_runs r ` +
		`ON r.deployment_id=d.id WHERE d.id=${Number(id)} ` +
		`ORDER BY r.step_index, r.agent_id LIMIT 1;`,
	);
	const waitClaim = (id, state) => waitFor(
		() => claim(id),
		(value) => value.startsWith(`${state}|`),
		`claim ${id} state ${state}`,
	);
	const waitStatus = (id, status) => waitFor(
		() => deploymentStatus(id),
		(value) => value.startsWith(`${status}|`),
		`deployment ${id} status ${status}`,
	);
	const curl = (path, body) => command("curl", [
		"--silent", "--show-error", "--http1.1",
		"--cacert", join(serverIdentity, "identity.crt"),
		"--cert", join(agentStateDir, "identity.crt"),
		"--key", join(agentStateDir, "identity.key"),
		"--header", "Content-Type: application/json",
		"--output", "/dev/null", "--write-out", "%{http_code}",
		"--data", body, `https://${lifecycleAddress}${path}`,
	]);
	const armResponse = (skip = 0) => {
		lifecycleProxy.arm({
			action: "gate",
			direction: "upstream-to-client",
			skip,
		});
		return lifecycleProxy.nextGate();
	};
	const armRequest = () => {
		lifecycleProxy.arm({
			action: "gate",
			direction: "client-to-upstream",
		});
		return lifecycleProxy.nextGate();
	};
	const successfulScript = "printf '%s\\n' remote-agent-ok";
	let checkpoint;

	if (scenario === "wrong-token") {
		await stopAgent();
		const deployment = await createDeployment(successfulScript);
		const status = await curl(
			`/agent/v1/deployments/${deployment.id}/start`,
			'{"protocol":"agent/1","claim_token":"wrong-token"}',
		);
		check(status === "409", `wrong token returned ${status}`);
		checkpoint = await waitClaim(deployment.id, "waiting");
	} else if (scenario === "duplicate-poll") {
		await stopAgent();
		const deployment = await createDeployment("exec sleep 90");
		const body = '{"protocol":"agent/1","agent_version":"todo12-proxy"}';
		const statuses = await Promise.all([
			curl("/agent/v1/poll", body),
			curl("/agent/v1/poll", body),
		]);
		statuses.sort();
		check(statuses.join("|") === "200|204",
			`duplicate polls returned ${statuses.join("|")}`);
		checkpoint = await waitClaim(deployment.id, "claimed");
		const firstHash = checkpoint.split("|")[2];
		await waitClaim(deployment.id, "waiting");
		await restartAgent();
		await waitFor(
			() => claim(deployment.id),
			(value) => value.split("|")[2] !== firstHash &&
				Number(value.split("|")[3]) > 0,
			"reclaimed deployment start",
		);
		await stopAgent();
	} else if (scenario === "pre-start-loss" || scenario === "lost-poll-response") {
		const gatePromise = armResponse();
		const deployment = await createDeployment(
			scenario === "pre-start-loss" ? "exec sleep 90" : successfulScript,
		);
		const gate = await gatePromise;
		checkpoint = await waitClaim(deployment.id, "claimed");
		if (scenario === "pre-start-loss") await stopAgent();
		gate.drop();
		if (scenario === "pre-start-loss") {
			const firstHash = checkpoint.split("|")[2];
			await waitClaim(deployment.id, "waiting");
			await restartAgent();
			await waitFor(
				() => claim(deployment.id),
				(value) => value.split("|")[2] !== firstHash &&
					Number(value.split("|")[3]) > 0,
				"reclaimed deployment start",
			);
			await stopAgent();
		} else {
			await waitStatus(deployment.id, "succeeded");
		}
	} else if (scenario === "lost-start-response") {
		const gatePromise = armResponse(1);
		const deployment = await createDeployment(successfulScript);
		const gate = await gatePromise;
		checkpoint = await waitClaim(deployment.id, "started");
		gate.drop();
		await waitStatus(deployment.id, "succeeded");
	} else if (scenario === "post-start-loss") {
		const deployment = await createDeployment("exec sleep 90");
		checkpoint = await waitClaim(deployment.id, "started");
		await stopAgent();
		await waitClaim(deployment.id, "lost");
		await restartAgent();
		await new Promise((resolve) => setTimeout(resolve, 1500));
		check((await claim(deployment.id)).trim().startsWith("lost||"),
			"lost deployment replayed after agent restart");
	} else if (scenario === "cancel-unconfirmed") {
		const deployment = await createDeployment("exec sleep 90");
		await waitClaim(deployment.id, "started");
		const requestGate = await armRequest();
		await api("POST", `/deployments/${deployment.id}/cancel`);
		const gatePromise = armResponse();
		requestGate.forward();
		const gate = await gatePromise;
		checkpoint = await waitClaim(deployment.id, "cancel_requested");
		await stopAgent();
		gate.drop();
		await waitClaim(deployment.id, "cancel_unconfirmed");
	} else if (scenario === "lost-heartbeat-response") {
		const deployment = await createDeployment("sleep 12");
		await waitClaim(deployment.id, "started");
		const gatePromise = armResponse();
		const gate = await gatePromise;
		checkpoint = await waitFor(
			() => claim(deployment.id),
			(value) => {
				const fields = value.split("|");
				return Number(fields[4]) > Number(fields[3]);
			},
			"committed heartbeat",
		);
		gate.drop();
		await waitStatus(deployment.id, "succeeded");
	} else if (scenario === "lost-logs-response") {
		const deployment = await createDeployment("sleep 2; echo LOG_MARKER; sleep 2");
		await waitClaim(deployment.id, "started");
		const gatePromise = armResponse();
		const gate = await gatePromise;
		checkpoint = await waitFor(
			() => readOnly(`SELECT COUNT(*) FROM deployment_logs ` +
				`WHERE deployment_id=${deployment.id} AND line='LOG_MARKER';`),
			(value) => value === "1",
			"committed log batch",
		);
		gate.drop();
		await waitStatus(deployment.id, "succeeded");
		check((await readOnly(`SELECT COUNT(*) FROM deployment_logs ` +
			`WHERE deployment_id=${deployment.id} AND line='LOG_MARKER';`)).trim() === "1",
		"lost log response duplicated a durable line");
	} else if (scenario === "lost-result-response") {
		const deployment = await createDeployment("sleep 2; echo RESULT_MARKER");
		await waitClaim(deployment.id, "started");
		const gatePromise = armResponse(1);
		const gate = await gatePromise;
		checkpoint = await waitClaim(deployment.id, "succeeded");
		gate.drop();
		await waitStatus(deployment.id, "succeeded");
		check((await readOnly(`SELECT COUNT(*) FROM notification_events ` +
			`WHERE deployment_id=${deployment.id};`)).trim() === "1",
		"lost result response duplicated completion notification");
	} else if (scenario === "lost-cancelled-response") {
		await stopAgent();
		const client = await createFaultClient({
			address: lifecycleAddress,
			serverIdentity,
			stateDir: agentStateDir,
		});
		try {
			const deployment = await createDeployment(successfulScript);
			const polled = await client.post("/agent/v1/poll",
				'{"protocol":"agent/1","agent_version":"todo12-proxy"}');
			check(polled.status === 200, `manual poll returned ${polled.status}`);
			const claimed = JSON.parse(polled.body);
			const lifecycleBody = JSON.stringify({
				protocol: "agent/1",
				claim_token: claimed.claim_token,
			});
			const started = await client.post(
				`/agent/v1/deployments/${deployment.id}/start`, lifecycleBody);
			check(started.status === 204, `manual start returned ${started.status}`);
			await waitClaim(deployment.id, "started");
			await api("POST", `/deployments/${deployment.id}/cancel`);
			await waitClaim(deployment.id, "cancel_requested");
			const gatePromise = armResponse();
			const firstAttempt = client.post(
				`/agent/v1/deployments/${deployment.id}/cancelled`, lifecycleBody,
			).catch((error) => error);
			const gate = await gatePromise;
			checkpoint = await waitClaim(deployment.id, "cancelled");
			gate.drop();
			check(await firstAttempt instanceof Error,
				"lost cancelled response unexpectedly reached the client");
			const retry = await client.post(
				`/agent/v1/deployments/${deployment.id}/cancelled`, lifecycleBody);
			check(retry.status === 204, `cancelled retry returned ${retry.status}`);
			await waitStatus(deployment.id, "cancelled");
		} finally {
			client.close();
		}
	}

	return {
		checkpoint,
		proxyEvents: lifecycleProxy.transcript.length,
		scenario,
	};
}
