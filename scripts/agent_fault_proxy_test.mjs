import assert from "node:assert/strict";
import net from "node:net";
import test from "node:test";

import { FaultProxy } from "./agent_fault_proxy.mjs";
import { verifyExactEvents } from "./agent_e2e_verify.mjs";

async function listen(server) {
	await new Promise((resolve, reject) => {
		server.once("error", reject);
		server.listen(0, "127.0.0.1", resolve);
	});
	const address = server.address();
	assert(address && typeof address !== "string");
	return `127.0.0.1:${address.port}`;
}

async function connect(address) {
	const [host, port] = address.split(":");
	const socket = net.connect(Number(port), host);
	await new Promise((resolve, reject) => {
		socket.once("connect", resolve);
		socket.once("error", reject);
	});
	return socket;
}

async function close(server) {
	await new Promise((resolve, reject) => {
		server.close((error) => error ? reject(error) : resolve());
	});
}

function readOnce(socket) {
	return new Promise((resolve, reject) => {
		socket.once("data", resolve);
		socket.once("error", reject);
	});
}

test("FaultProxy forwards opaque bytes and records metadata only", async (t) => {
	const upstream = net.createServer((socket) => socket.pipe(socket));
	const upstreamAddress = await listen(upstream);
	const proxy = await FaultProxy.start({ upstreamAddress });
	t.after(async () => {
		await proxy.close();
		await close(upstream);
	});

	const client = await connect(proxy.address);
	t.after(() => client.destroy());
	client.write("claim_token=must-not-leak");
	assert.equal((await readOnce(client)).toString(), "claim_token=must-not-leak");
	assert(proxy.transcript.some((event) => event.direction === "client-to-upstream"));
	assert(!JSON.stringify(proxy.transcript).includes("must-not-leak"));
});

test("FaultProxy gates then drops an upstream response", async (t) => {
	const upstream = net.createServer((socket) => {
		socket.once("data", () => socket.write("complete-response"));
	});
	const upstreamAddress = await listen(upstream);
	const proxy = await FaultProxy.start({ upstreamAddress });
	t.after(async () => {
		await proxy.close();
		await close(upstream);
	});
	proxy.arm({ direction: "upstream-to-client", action: "gate" });

	const client = await connect(proxy.address);
	t.after(() => client.destroy());
	client.write("request");
	const gate = await proxy.nextGate();
	assert.equal(gate.bytes, Buffer.byteLength("complete-response"));
	gate.drop();
	await new Promise((resolve) => client.once("close", resolve));
	assert(proxy.transcript.some((event) => event.action === "drop"));
});

test("FaultProxy truncates a selected direction at the exact byte count", async (t) => {
	const upstream = net.createServer((socket) => socket.pipe(socket));
	const upstreamAddress = await listen(upstream);
	const proxy = await FaultProxy.start({ upstreamAddress });
	t.after(async () => {
		await proxy.close();
		await close(upstream);
	});
	proxy.arm({
		direction: "upstream-to-client",
		action: "truncate",
		bytes: 4,
	});

	const client = await connect(proxy.address);
	t.after(() => client.destroy());
	client.write("abcdefgh");
	assert.equal((await readOnce(client)).toString(), "abcd");
	await new Promise((resolve) => client.once("close", resolve));
	assert(proxy.transcript.some((event) =>
		event.action === "truncate" && event.forwardedBytes === 4));
});

test("FaultProxy skips matching chunks before applying a gate", async (t) => {
	const upstream = net.createServer((socket) => {
		socket.on("data", (chunk) => socket.write(chunk));
	});
	const upstreamAddress = await listen(upstream);
	const proxy = await FaultProxy.start({ upstreamAddress });
	t.after(async () => {
		await proxy.close();
		await close(upstream);
	});
	proxy.arm({ direction: "upstream-to-client", action: "gate", skip: 1 });

	const client = await connect(proxy.address);
	t.after(() => client.destroy());
	client.write("first");
	assert.equal((await readOnce(client)).toString(), "first");
	client.write("second");
	const gate = await proxy.nextGate();
	assert.equal(gate.bytes, Buffer.byteLength("second"));
	gate.forward();
	assert.equal((await readOnce(client)).toString(), "second");
});

test("verifyExactEvents accepts exactly one ordered PASS per requirement", () => {
	assert.deepEqual(
		verifyExactEvents("PASS first\nPASS second\n", ["first", "second"]),
		["first", "second"],
	);
});

for (const [name, transcript] of [
	["missing", "PASS first\n"],
	["duplicate", "PASS first\nPASS first\nPASS second\n"],
	["unexpected", "PASS first\nPASS surprise\n"],
	["out-of-order", "PASS second\nPASS first\n"],
	["failed", "PASS first\nFAIL second\n"],
	["skipped", "PASS first\nSKIP second\n"],
]) {
	test(`verifyExactEvents rejects ${name} events`, () => {
		assert.throws(
			() => verifyExactEvents(transcript, ["first", "second"]),
			/strict event mismatch|forbidden event/,
		);
	});
}
