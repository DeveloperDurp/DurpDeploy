#!/usr/bin/env node
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { ManifestError, validateManifest } from "./auth_mfa_attack_manifest_rules.mjs";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const defaultManifest = resolve(root, "scripts/auth_mfa_attack_manifest.json");

function main() {
	const manifestPath = process.argv[2] ?? defaultManifest;
	const manifest = JSON.parse(readFileSync(manifestPath, "utf8"));
	const server = readFileSync(resolve(root, "internal/server/server.go"), "utf8");
	const count = validateManifest(manifest, server);
	console.log(`auth/MFA attack manifest: OK (${count} scenarios)`);
}

try {
	main();
} catch (error) {
	const message = error instanceof ManifestError || error instanceof SyntaxError
		? error.message
		: "unexpected failure";
	console.error(`auth/MFA attack manifest: ${message}`);
	process.exitCode = 1;
}
