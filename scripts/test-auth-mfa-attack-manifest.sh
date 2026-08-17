#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
VALIDATOR="$ROOT_DIR/scripts/check-auth-mfa-attack-manifest.mjs"
MANIFEST="$ROOT_DIR/scripts/auth_mfa_attack_manifest.json"
TMP=$(mktemp -d)

cleanup() {
	rm -rf "$TMP"
}
trap cleanup EXIT

fail() {
	echo "auth/MFA attack manifest test: $1" >&2
	exit 1
}

node "$VALIDATOR" "$MANIFEST"

make_fixture() {
	local kind=$1
	local output="$TMP/$kind.json"

	node - "$MANIFEST" "$output" "$kind" <<'NODE'
const fs = require("node:fs");

const [input, output, kind] = process.argv.slice(2);
const manifest = JSON.parse(fs.readFileSync(input, "utf8"));

switch (kind) {
case "duplicate-id":
	manifest.scenarios.push({ ...manifest.scenarios[0] });
	break;
case "missing-route":
	delete manifest.scenarios[0].route;
	break;
case "missing-durable-state":
	delete manifest.scenarios[0].durable_state;
	break;
case "missing-audit":
	delete manifest.scenarios[0].audit;
	break;
case "unsupported-layer":
	manifest.scenarios[0].layer = "unsupported";
	break;
case "unsupported-engine":
	manifest.scenarios[0].engine = "unsupported";
	break;
case "secret-artifact-metadata":
	manifest.scenarios[0].artifacts.metadata.push("password");
	break;
case "secret-artifact-field":
	manifest.scenarios[0].artifacts.password = "redacted-placeholder";
	break;
case "protocol-artifact-metadata":
	manifest.scenarios.find((scenario) => scenario.id === "oidc-valid-login")
		.artifacts.metadata.push("state");
	break;
case "protocol-artifact-field":
	manifest.scenarios.find((scenario) => scenario.id === "oidc-valid-login")
		.artifacts.nonce = "redacted-placeholder";
	break;
case "unassigned-server-route":
	manifest.route_inventory = manifest.route_inventory.slice(1);
	break;
case "missing-runtime-owner":
	manifest.execution_registry[0].scenario_ids = manifest.execution_registry[0].scenario_ids.slice(1);
	break;
case "mismatched-runtime-owner":
	manifest.execution_registry[0].owner = "scripts/not-a-runner.sh";
	break;
case "missing-oidc-scenario":
	manifest.scenarios = manifest.scenarios.filter(
		(scenario) => scenario.id !== "oidc-disabled-regression",
	);
	break;
case "wrong-oidc-runtime-owner": {
	const oidc = manifest.execution_registry.find(
		(entry) => entry.owner === "scripts/oidc_http_matrix.sh",
	);
	oidc.owner = "scripts/not-an-oidc-runner.sh";
	for (const id of oidc.scenario_ids) {
		manifest.scenarios.find((scenario) => scenario.id === id).owner = oidc.owner;
	}
	break;
}
default:
	throw new Error(`unknown fixture kind: ${kind}`);
}

fs.writeFileSync(output, `${JSON.stringify(manifest, null, 2)}\n`);
NODE

	printf '%s\n' "$output"
}

expect_invalid() {
	local kind=$1
	local expected=$2
	local fixture
	local output

	fixture=$(make_fixture "$kind")
	if output=$(node "$VALIDATOR" "$fixture" 2>&1); then
		fail "$kind fixture unexpectedly passed"
	fi
	printf '%s' "$output" | grep -Fq -- "$expected" ||
		fail "$kind fixture did not report: $expected"
}

expect_invalid duplicate-id 'duplicate scenario id'
expect_invalid missing-route 'missing required field route'
expect_invalid missing-durable-state 'missing required field durable_state'
expect_invalid missing-audit 'missing required field audit'
expect_invalid unsupported-layer 'unsupported layer'
expect_invalid unsupported-engine 'unsupported engine'
expect_invalid secret-artifact-metadata 'secret-bearing artifact metadata'
expect_invalid secret-artifact-field 'secret-bearing artifact field'
expect_invalid protocol-artifact-metadata 'protocol-bearing artifact metadata'
expect_invalid protocol-artifact-field 'protocol-bearing artifact field'
expect_invalid unassigned-server-route 'registered in server.go but absent from route_inventory'
expect_invalid missing-runtime-owner 'missing execution ownership'
expect_invalid mismatched-runtime-owner 'owner disagrees with execution ownership'
expect_invalid missing-oidc-scenario 'missing required OIDC scenario oidc-disabled-regression'
expect_invalid wrong-oidc-runtime-owner 'must be owned by scripts/oidc_http_matrix.sh'

echo "auth/MFA attack manifest validator test: OK"
