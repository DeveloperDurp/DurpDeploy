import { promises as fs } from "node:fs";
import { spawn } from "node:child_process";
import { join } from "node:path";
import { runFullRoutingProof } from "./agent_full_routing_proof.mjs";

export async function runFullRoutingOrchestration(outputDir) {

	await runFullRoutingProof(join(outputDir, "real-routing"));
	for (const childScenario of ["labels", "fanout"]) {
		await new Promise((resolve, reject) => {
			const child = spawn(process.execPath, ["scripts/agent_admin_browser_proof.mjs", "--scenario", childScenario], {
				env: { ...process.env, AGENT_BROWSER_OUTPUT_DIR: join(outputDir, childScenario) }, stdio: "inherit",
			});
			const deadline = setTimeout(() => child.kill("SIGTERM"), 180000);
			child.once("error", reject);
			child.once("exit", (code) => {
				clearTimeout(deadline);
				code === 0 ? resolve() : reject(new Error(`${childScenario} exited ${code}`));
			});
		});
	}
	const consoles = [];
	const cleanups = [];
	for (const name of ["real-routing", "labels", "fanout"]) {
		consoles.push(JSON.parse(await fs.readFile(join(outputDir, name, "browser-console.json"), "utf8")));
		cleanups.push(JSON.parse(await fs.readFile(join(outputDir, name, "cleanup.json"), "utf8")));
	}
	if (consoles.some((receipt) => receipt.errors.length)) throw new Error("browser console errors");
	await fs.writeFile(join(outputDir, "browser-console.json"), JSON.stringify({ errors: [] }));
	await fs.writeFile(join(outputDir, "cleanup.json"), JSON.stringify({ browserClosed: true, complete: true, scenarios: cleanups }));
	const scanned = [];
	for (const name of await fs.readdir(outputDir, { recursive: true })) {
		const path = join(outputDir, name);
		if (!(await fs.stat(path)).isFile() || name.endsWith(".png")) continue;
		const contents = await fs.readFile(path, "utf8");
		if (/ddp_pat_[A-Za-z0-9_-]+|-----BEGIN .*PRIVATE KEY-----|claim_token["=:]/.test(contents)) throw new Error(`secret scan failed: ${name}`);
		scanned.push(name);
	}
	await fs.writeFile(join(outputDir, "secret-scan.json"), JSON.stringify({ passed: true, scanned }));
	console.log("browser secret scan: PASS");
	console.log("full-routing labels, fanout, real-agent browser scenarios: PASS");
}
