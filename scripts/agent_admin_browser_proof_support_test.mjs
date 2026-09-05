import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import test from "node:test";
import {
	attachPageDiagnostics,
	findUnexpectedConsoleErrors,
	isExplicitlyEmptyResponse,
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
