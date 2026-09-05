import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import test from "node:test";
import {
	attachPageDiagnostics,
	findUnexpectedConsoleErrors,
	isExplicitlyEmptyResponse,
	reserveHarnessAddresses,
	waitForSettledRename,
} from "./agent_admin_browser_proof_support.mjs";

const baseURL = "http://127.0.0.1:18081";
const resourceError = (status, path) => ({
	text: `Failed to load resource: the server responded with a status of ${status} (Conflict)`,
	url: new URL(path, baseURL).href,
});

test("consumes one console error for one exact expected HTTP failure", () => {
	const errors = [resourceError(409, "/admin/agent-labels")];
	const expected = [{ method: "POST", path: "/admin/agent-labels", status: 409 }];

	assert.deepEqual(findUnexpectedConsoleErrors(errors, expected, baseURL), []);
});

test("keeps malformed and unexpected failures fatal", () => {
	const expected = [{ method: "POST", path: "/admin/agent-labels", status: 409 }];
	const errors = [
		{ text: "Failed to load resource: status unavailable", url: `${baseURL}/admin/agent-labels` },
		resourceError(404, "/admin/agent-labels"),
		resourceError(500, "/admin/agent-labels"),
		resourceError(409, "/admin/other"),
		resourceError(409, "/admin/agent-labels"),
		resourceError(409, "/admin/agent-labels"),
	];

	assert.deepEqual(findUnexpectedConsoleErrors(errors, expected, baseURL), [
		errors[0], errors[1], errors[2], errors[3], errors[5],
	]);
});

test("does not classify an expected status without an exact resource URL", () => {
	const errors = [{
		text: "Failed to load resource: the server responded with a status of 409 (Conflict)",
		url: "",
	}];
	const expected = [{ method: "DELETE", path: "/admin/agent-labels/7", status: 409 }];

	assert.deepEqual(findUnexpectedConsoleErrors(errors, expected, baseURL), errors);
});

test("attaches diagnostics once when page discovery and setup both register it", () => {
	const page = new EventEmitter();
	const errors = [];
	attachPageDiagnostics(page, errors);
	attachPageDiagnostics(page, errors);
	page.emit("console", {
		location: () => ({ url: `${baseURL}/admin/agent-labels` }),
		text: () => "unexpected",
		type: () => "error",
	});

	assert.deepEqual(errors, [{
		text: "unexpected",
		url: `${baseURL}/admin/agent-labels`,
	}]);
});

test("requires an explicit zero-length non-streaming response", () => {
	assert.equal(isExplicitlyEmptyResponse({ "content-length": "0" }), true);
	assert.equal(isExplicitlyEmptyResponse({ "content-length": "2" }), false);
	assert.equal(isExplicitlyEmptyResponse({}), false);
	assert.equal(isExplicitlyEmptyResponse({
		"content-length": "0",
		"transfer-encoding": "chunked",
	}), false);
});

test("waits for renamed DOM state when the URL is already current", async () => {
	const calls = [];
	const page = {
		getByLabel: () => ({
			inputValue: async () => "Cat Facts",
			waitFor: async () => { calls.push("input"); },
		}),
		getByRole: () => ({
			isVisible: async () => true,
			waitFor: async () => { calls.push("heading"); },
		}),
		waitForURL: () => { throw new Error("same URL is not a completion signal"); },
	};

	assert.deepEqual(await waitForSettledRename(page, "Cat Facts"), {
		headingVisible: true,
		inputValue: "Cat Facts",
	});
	assert.deepEqual(calls, ["heading", "input"]);
});

test("reserves unique harness addresses and releases every reservation", async () => {
	const released = [];
	let port = 24000;
	const reservations = await reserveHarnessAddresses(
		[undefined, undefined, "127.0.0.1:25000"],
		async (requested) => {
			const address = requested ?? `127.0.0.1:${port++}`;
			return { address, release: async () => { released.push(address); } };
		},
	);

	assert.deepEqual(reservations.addresses, [
		"127.0.0.1:24000", "127.0.0.1:24001", "127.0.0.1:25000",
	]);
	await reservations.release();
	assert.deepEqual(released, reservations.addresses);
});

test("releases prior reservations and names a configured port collision", async () => {
	const released = [];
	const reserve = async (requested) => {
		if (requested === "127.0.0.1:25000") throw new Error("EADDRINUSE");
		return {
			address: "127.0.0.1:24000",
			release: async () => { released.push("127.0.0.1:24000"); },
		};
	};

	await assert.rejects(
		reserveHarnessAddresses([undefined, "127.0.0.1:25000"], reserve),
		/address 127\.0\.0\.1:25000 is unavailable: EADDRINUSE/,
	);
	assert.deepEqual(released, ["127.0.0.1:24000"]);
});
