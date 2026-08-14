const requiredFamilies = new Set([
	"login_session",
	"csrf_viewer_role",
	"pending_mfa",
	"challenge_binding_expiry_replay_throttle",
	"totp",
	"recovery",
	"webauthn_passkeys",
	"reauth",
	"factor_lifecycle",
	"admin_reset",
	"api_separation",
	"cache_secret_safety",
	"database_parity",
]);
const requiredExclusions = new Set([
	"excluded-endpoint-compromise",
	"excluded-infrastructure-compromise",
	"excluded-api-token-mfa-enforcement",
]);
const allowed = {
	layers: new Set(["http", "browser", "database"]),
	engines: new Set(["sqlite", "postgres", "mssql"]),
	scopes: new Set(["full", "parity"]),
	coverageStatuses: new Set(["existing-partial", "existing-lower-level", "planned"]),
	coverageSources: new Set([
		"shell-e2e",
		"browser-e2e",
		"handler-test",
		"mfa-unit",
		"database-parity",
		"planned-todo-4",
		"planned-todo-5",
		"planned-todo-6",
		"planned-todo-7",
	]),
};
const runtimeSources = new Set(["shell-e2e", "browser-e2e"]);
const secretBearingName = /password|token|secret|seed|recovery|challenge|assertion|credential|session|cookie|webauthn|passkey|blob/i;

export class ManifestError extends Error {
	constructor(message) {
		super(message);
		this.name = "ManifestError";
	}
}

function fail(message) {
	throw new ManifestError(message);
}

function record(value) {
	return value !== null && typeof value === "object" && !Array.isArray(value);
}

function text(value, field, context) {
	if (typeof value[field] !== "string" || value[field].trim() === "") {
		fail(`${context}: missing required field ${field}`);
	}
	return value[field];
}

export function routeKey(value, context) {
	const method = text(value, "method", context);
	const route = text(value, "route", context);
	if (!/^(GET|POST|PUT|PATCH|DELETE)$/.test(method)) {
		fail(`${context}: unsupported method ${method}`);
	}
	if (!route.startsWith("/")) {
		fail(`${context}: route must start with /`);
	}
	return `${method} ${route}`;
}

function routes(source, pattern, prefix = "") {
	const result = new Set();
	const matcher = /\.(Get|Post|Put|Patch|Delete)\s*\(\s*"([^"]+)"/g;
	for (const [, method, route] of source.matchAll(matcher)) {
		if (pattern.test(route)) result.add(`${method.toUpperCase()} ${prefix}${route}`);
	}
	return result;
}

function registeredRoutes(source) {
	const apiStart = source.indexOf('r.Route("/api/v1"');
	if (apiStart < 0) fail("internal/server/server.go has no API v1 route group");
	const browser = routes(
		source.slice(0, apiStart),
		/^\/login(?:\/mfa(?:\/.*)?)?$|^\/logout$|^\/projects$|^\/settings\/(?:tokens|security)(?:\/.*)?$|^\/admin\/(?:tokens(?:\/.*)?|users\/\{id\}\/mfa-reset)$/,
	);
	const api = routes(
		source.slice(apiStart),
		/^\/(?:tokens(?:\/.*)?|admin\/tokens(?:\/.*)?|users\/me|projects)$/,
		"/api/v1",
	);
	return new Set([...browser, ...api]);
}

function artifacts(value, context) {
	if (!record(value)) fail(`${context}: missing required field artifacts`);
	for (const name of Object.keys(value)) {
		if (secretBearingName.test(name)) fail(`${context}: secret-bearing artifact field ${name}`);
	}
	for (const field of ["metadata", "on_failure"]) {
		if (!Array.isArray(value[field])) fail(`${context}: artifact ${field} must be an array`);
		for (const name of value[field]) {
			if (typeof name !== "string" || name.trim() === "") {
				fail(`${context}: artifact ${field} must contain names`);
			}
			if (secretBearingName.test(name)) {
				fail(`${context}: secret-bearing artifact metadata ${name}`);
			}
		}
	}
}

function coverage(value, context) {
	if (!record(value)) fail(`${context}: missing required field coverage`);
	const status = text(value, "status", context);
	const source = text(value, "source", context);
	if (!allowed.coverageStatuses.has(status)) fail(`${context}: unsupported coverage status ${status}`);
	if (!allowed.coverageSources.has(source)) fail(`${context}: unsupported coverage source ${source}`);
	if (typeof value.runtime_e2e !== "boolean") {
		fail(`${context}: coverage.runtime_e2e must be boolean`);
	}
	if (value.runtime_e2e !== runtimeSources.has(source)) {
		fail(`${context}: runtime_e2e disagrees with coverage source ${source}`);
	}
	if (status === "planned" && value.runtime_e2e) {
		fail(`${context}: planned scenario cannot claim runtime E2E coverage`);
	}
}

function scenario(value, index, inventory) {
	const context = `scenario ${index}`;
	if (!record(value)) fail(`${context}: must be an object`);
	const id = text(value, "id", context);
	if (!/^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(id)) {
		fail(`${context}: id must be machine-readable`);
	}
	const family = text(value, "family", context);
	for (const field of ["owner", "attacker_state", "protocol", "durable_state", "audit"]) {
		text(value, field, context);
	}
	const primary = routeKey(value, context);
	if (!inventory.has(primary)) fail(`${context}: route ${primary} is absent from route_inventory`);
	for (const [field, options] of Object.entries({
		layer: allowed.layers,
		engine: allowed.engines,
		matrix_scope: allowed.scopes,
	})) {
		if (!options.has(value[field])) fail(`${context}: unsupported ${field} ${value[field]}`);
	}
	if (value.engine === "sqlite" && value.matrix_scope !== "full") {
		fail(`${context}: SQLite scenarios must use the full matrix`);
	}
	if (value.engine !== "sqlite" && value.matrix_scope !== "parity") {
		fail(`${context}: non-SQLite scenarios must use the parity subset`);
	}
	coverage(value.coverage, context);
	artifacts(value.artifacts, context);
	if (!Array.isArray(value.covered_routes) || value.covered_routes.length === 0) {
		fail(`${context}: covered_routes must be a non-empty array`);
	}
	const covered = new Set(value.covered_routes.map((route, routeIndex) => {
		const key = routeKey(route, `${context} covered route ${routeIndex}`);
		if (!inventory.has(key)) fail(`${context}: covered route ${key} is absent from route_inventory`);
		return key;
	}));
	if (!covered.has(primary)) fail(`${context}: covered_routes must include the primary route`);
	return {
		family, id, owner: value.owner, layer: value.layer,
		engine: value.engine, runtimeE2E: value.coverage.runtime_e2e,
	};
}

function executionRegistry(manifest, scenarios) {
	if (!Array.isArray(manifest.execution_registry)) fail("missing required field execution_registry");
	const ownership = new Map();
	for (const [index, entry] of manifest.execution_registry.entries()) {
		const context = `execution_registry ${index}`;
		if (!record(entry)) fail(`${context}: must be an object`);
		const owner = text(entry, "owner", context);
		const kind = text(entry, "kind", context);
		if (!new Set(["runtime", "declarative-only", "parity"]).has(kind)) {
			fail(`${context}: unsupported kind ${kind}`);
		}
		if (typeof entry.emits_ids !== "boolean") fail(`${context}: emits_ids must be boolean`);
		if (entry.emits_ids !== (kind === "runtime")) fail(`${context}: emits_ids must match execution kind`);
		if (!Array.isArray(entry.scenario_ids) || entry.scenario_ids.length === 0) {
			fail(`${context}: scenario_ids must be a non-empty array`);
		}
		for (const id of entry.scenario_ids) {
			if (typeof id !== "string") fail(`${context}: scenario_ids must contain strings`);
			if (ownership.has(id)) fail(`${context}: duplicate execution owner for ${id}`);
			ownership.set(id, { kind, owner });
		}
	}
	for (const scenario of scenarios) {
		const execution = ownership.get(scenario.id);
		if (!execution) fail(`scenario ${scenario.id}: missing execution ownership`);
		if (execution.owner !== scenario.owner) fail(`scenario ${scenario.id}: owner disagrees with execution ownership`);
		if (scenario.engine === "sqlite" && scenario.layer !== "database" && scenario.runtimeE2E && execution.kind !== "runtime") {
			fail(`scenario ${scenario.id}: SQLite ${scenario.layer} route coverage requires a runtime owner`);
		}
		if (scenario.engine !== "sqlite" && execution.kind !== "parity") {
			fail(`scenario ${scenario.id}: non-SQLite scenario requires parity execution ownership`);
		}
	}
}

export function validateManifest(manifest, source) {
	if (!record(manifest)) fail("manifest must be an object");
	if (manifest.version !== 1) fail("manifest version must be 1");
	if (!record(manifest.contract)) fail("missing required field contract");
	if (manifest.contract.api_bearer_tokens !== "mfa-exempt-machine-credentials") {
		fail("API bearer tokens must remain MFA-exempt machine credentials");
	}
	if (!Array.isArray(manifest.route_inventory)) fail("missing required field route_inventory");
	const inventory = new Set();
	for (const [index, route] of manifest.route_inventory.entries()) {
		const key = routeKey(route, `route_inventory ${index}`);
		if (inventory.has(key)) fail(`duplicate route_inventory entry ${key}`);
		inventory.add(key);
	}
	const registered = registeredRoutes(source);
	for (const key of registered) {
		if (!inventory.has(key)) fail(`${key} is registered in server.go but absent from route_inventory`);
	}
	for (const key of inventory) {
		if (!registered.has(key)) fail(`${key} is not an approved in-scope auth/MFA route`);
	}
	if (!Array.isArray(manifest.scenarios)) fail("missing required field scenarios");
	const ids = new Set();
	const families = new Set();
	const scenarios = [];
	for (const [index, value] of manifest.scenarios.entries()) {
		const result = scenario(value, index, inventory);
		if (ids.has(result.id)) fail(`duplicate scenario id ${result.id}`);
		ids.add(result.id);
		families.add(result.family);
		scenarios.push(result);
	}
	executionRegistry(manifest, scenarios);
	for (const family of requiredFamilies) {
		if (!families.has(family)) fail(`missing required scenario family ${family}`);
	}
	for (const key of inventory) {
		if (!manifest.scenarios.some((value) => value.covered_routes.some(
			(route) => routeKey(route, `scenario ${value.id}`) === key,
		))) fail(`${key} has no scenario assignment`);
	}
	for (const layer of ["http", "browser"]) {
		if (!manifest.scenarios.some((value) => value.layer === layer && value.engine === "sqlite")) {
			fail(`SQLite full matrix is missing a ${layer} scenario`);
		}
	}
	for (const engine of ["postgres", "mssql"]) {
		if (!manifest.scenarios.some((value) => value.layer === "database" && value.engine === engine)) {
			fail(`${engine} parity subset is missing`);
		}
	}
	if (!manifest.scenarios.some((value) => value.api_token_policy === "mfa-exempt-machine-credential")) {
		fail("manifest must include the MFA-exempt API bearer-token scenario");
	}
	if (!Array.isArray(manifest.exclusions)) fail("missing required field exclusions");
	const exclusions = new Set();
	for (const [index, value] of manifest.exclusions.entries()) {
		const context = `exclusion ${index}`;
		if (!record(value)) fail(`${context}: must be an object`);
		const id = text(value, "id", context);
		for (const field of ["threat", "reason"]) text(value, field, context);
		if (exclusions.has(id)) fail(`duplicate exclusion id ${id}`);
		exclusions.add(id);
	}
	for (const id of requiredExclusions) {
		if (!exclusions.has(id)) fail(`missing explicit exclusion ${id}`);
	}
	return manifest.scenarios.length;
}
