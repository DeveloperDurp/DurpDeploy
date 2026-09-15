import { EventEmitter, once } from "node:events";
import net from "node:net";

function endpoint(address) {
	const separator = address.lastIndexOf(":");
	if (separator < 1) throw new Error(`invalid TCP address: ${address}`);
	const host = address.slice(0, separator).replace(/^\[|\]$/g, "");
	const port = Number(address.slice(separator + 1));
	if (!Number.isInteger(port) || port < 1 || port > 65535) {
		throw new Error(`invalid TCP address: ${address}`);
	}
	return { host, port };
}

export class FaultProxy {
	static async start({ upstreamAddress, listenHost = "127.0.0.1" }) {
		const proxy = new FaultProxy(upstreamAddress, listenHost);
		await proxy.start();
		return proxy;
	}

	constructor(upstreamAddress, listenHost) {
		this.upstream = endpoint(upstreamAddress);
		this.listenHost = listenHost;
		this.server = net.createServer((client) => this.accept(client));
		this.events = new EventEmitter();
		this.rules = [];
		this.sockets = new Set();
		this.transcript = [];
		this.connectionID = 0;
	}

	async start() {
		this.server.listen(0, this.listenHost);
		await once(this.server, "listening");
		const address = this.server.address();
		if (!address || typeof address === "string") {
			throw new Error("fault proxy did not bind a TCP address");
		}
		this.address = `${this.listenHost}:${address.port}`;
	}

	arm(rule) {
		if (!["client-to-upstream", "upstream-to-client"].includes(rule.direction)) {
			throw new Error(`invalid fault direction: ${rule.direction}`);
		}
		if (!["gate", "drop", "truncate"].includes(rule.action)) {
			throw new Error(`invalid fault action: ${rule.action}`);
		}
		if (rule.action === "truncate" &&
			(!Number.isInteger(rule.bytes) || rule.bytes < 0)) {
			throw new Error("truncate requires a non-negative byte count");
		}
		this.rules.push({ ...rule });
	}

	nextGate() {
		return once(this.events, "gate").then(([gate]) => gate);
	}

	accept(client) {
		const id = ++this.connectionID;
		const upstream = net.createConnection(this.upstream);
		this.sockets.add(client);
		this.sockets.add(upstream);
		this.record({ action: "connect", connection: id });
		const closeBoth = () => {
			client.destroy();
			upstream.destroy();
		};
		const forget = (socket) => {
			this.sockets.delete(socket);
			this.record({ action: "close", connection: id });
		};
		client.once("close", () => forget(client));
		upstream.once("close", () => forget(upstream));
		client.once("error", closeBoth);
		upstream.once("error", closeBoth);
		client.on("data", (chunk) => {
			this.forward(id, "client-to-upstream", client, upstream, chunk);
		});
		upstream.on("data", (chunk) => {
			this.forward(id, "upstream-to-client", upstream, client, chunk);
		});
	}

	forward(connection, direction, source, destination, chunk) {
		const ruleIndex = this.rules.findIndex((rule) =>
			rule.direction === direction &&
			(rule.connection === undefined || rule.connection === connection));
		let rule;
		if (ruleIndex >= 0) {
			const candidate = this.rules[ruleIndex];
			if ((candidate.skip ?? 0) > 0) {
				candidate.skip -= 1;
			} else {
				rule = this.rules.splice(ruleIndex, 1)[0];
			}
		}
		this.record({
			action: rule?.action ?? "forward",
			bytes: chunk.length,
			connection,
			direction,
		});
		if (!rule) {
			destination.write(chunk);
			return;
		}
		if (rule.action === "drop") {
			source.destroy();
			destination.destroy();
			return;
		}
		if (rule.action === "truncate") {
			const prefix = chunk.subarray(0, rule.bytes);
			if (prefix.length > 0) destination.write(prefix);
			this.record({
				action: "truncate",
				connection,
				direction,
				forwardedBytes: prefix.length,
			});
			source.destroy();
			destination.destroySoon();
			return;
		}
		source.pause();
		let settled = false;
		const settle = (action) => {
			if (settled) return;
			settled = true;
			this.record({ action, connection, direction });
			if (action === "forward") {
				destination.write(chunk);
				source.resume();
				return;
			}
			source.destroy();
			destination.destroy();
		};
		this.events.emit("gate", {
			bytes: chunk.length,
			connection,
			direction,
			drop: () => settle("drop"),
			forward: () => settle("forward"),
		});
	}

	record(event) {
		this.transcript.push({ sequence: this.transcript.length + 1, ...event });
	}

	async close() {
		for (const socket of this.sockets) socket.destroy();
		if (!this.server.listening) return;
		await new Promise((resolve, reject) => {
			this.server.close((error) => error ? reject(error) : resolve());
		});
	}
}
