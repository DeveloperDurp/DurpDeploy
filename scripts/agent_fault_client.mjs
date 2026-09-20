import https from "node:https";
import { readFile } from "node:fs/promises";
import { join } from "node:path";

export async function createFaultClient({ address, serverIdentity, stateDir }) {
	const [ca, cert, key] = await Promise.all([
		readFile(join(serverIdentity, "identity.crt")),
		readFile(join(stateDir, "identity.crt")),
		readFile(join(stateDir, "identity.key")),
	]);
	const agent = new https.Agent({ ca, cert, key, keepAlive: true, maxSockets: 1 });
	return {
		close: () => agent.destroy(),
		post: (path, body) => new Promise((resolve, reject) => {
			const request = https.request({
				agent,
				headers: { "Content-Type": "application/json" },
				host: address.split(":")[0],
				method: "POST",
				path,
				port: Number(address.split(":")[1]),
			}, (response) => {
				const chunks = [];
				response.on("data", (chunk) => chunks.push(chunk));
				response.once("end", () => resolve({
					body: Buffer.concat(chunks).toString("utf8"),
					status: response.statusCode,
				}));
			});
			request.once("error", reject);
			request.end(body);
		}),
	};
}
