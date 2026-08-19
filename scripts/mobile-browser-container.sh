#!/usr/bin/env bash
set -euo pipefail

artifact_dir=${MOBILE_ARTIFACT_DIR:-/artifacts}
run_id=${MOBILE_RUN_ID:-local-$(date -u +%Y%m%dT%H%M%SZ)-$$}
evidence_dir="$artifact_dir/$run_id"
receipt_name="mobile-readability-$run_id.json"
receipt_path=".omo/evidence/$receipt_name"
: "${MOBILE_BROWSER_NODE_MODULES:=/opt/mobile-browser/node_modules}"
export MOBILE_BROWSER_NODE_MODULES
export NODE_PATH="$MOBILE_BROWSER_NODE_MODULES"

copy_receipt() {
	if [ -f "$receipt_path" ]; then
		cp "$receipt_path" "$evidence_dir/$receipt_name"
	fi
}

trap copy_receipt EXIT

mkdir -p "$evidence_dir" static/swagger-ui
templ generate
cp /opt/mobile-browser/node_modules/swagger-ui-dist/swagger-ui-bundle.js \
	static/swagger-ui/
cp /opt/mobile-browser/node_modules/swagger-ui-dist/swagger-ui-standalone-preset.js \
	static/swagger-ui/
cp /opt/mobile-browser/node_modules/swagger-ui-dist/swagger-ui.css \
	static/swagger-ui/
cp /opt/mobile-browser/node_modules/swagger-ui-dist/favicon-32x32.png \
	static/swagger-ui/
cp /opt/mobile-browser/node_modules/swagger-ui-dist/favicon-16x16.png \
	static/swagger-ui/

if [ "${AGENT_ADMIN_BROWSER_PROOF:-}" = "1" ]; then
	AGENT_BROWSER_EVIDENCE_DIR="$evidence_dir" \
	node /opt/mobile-browser/agent_admin_browser_proof.mjs
	exit 0
fi

MOBILE_EVIDENCE_DIR="$evidence_dir" \
MOBILE_EVIDENCE_FILE="$receipt_name" \
go test -tags=mobilebrowser -run '^TestMobileBrowserReadability$' \
	-count=1 -v ./internal/handler
