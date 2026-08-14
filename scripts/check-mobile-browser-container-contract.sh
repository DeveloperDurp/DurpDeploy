#!/usr/bin/env bash
set -euo pipefail

root=${MOBILE_BROWSER_CONTRACT_ROOT:-.}

require_file() {
	if [ ! -f "$root/$1" ]; then
		echo "mobile browser contract: missing $1" >&2
		exit 1
	fi
}

require_text() {
	local file=$1 text=$2 description=$3
	if ! grep -Fq -- "$text" "$root/$file"; then
		echo "mobile browser contract: $description" >&2
		exit 1
	fi
}

active_source() {
	local language=$1 file=$2
	case "$language" in
	docker)
		sed '/^[[:space:]]*#/d' "$file"
		;;
	go | js)
		awk '
			BEGIN { in_block = 0 }
			{
				line = $0
				while (1) {
					if (in_block) {
						end = index(line, "*/")
						if (!end) {
							line = ""
							break
						}
						line = substr(line, end + 2)
						in_block = 0
					}
					start = index(line, "/*")
					slash = index(line, "//")
					if (slash && (!start || slash < start)) {
						line = substr(line, 1, slash - 1)
						break
					}
					if (start) {
						line = substr(line, 1, start - 1)
						in_block = 1
						continue
					}
					break
				}
				if (line != "") print line
			}
		' "$file"
		;;
	*)
		echo "mobile browser contract: unsupported source language $language" >&2
		exit 1
		;;
	esac
}

require_active_text() {
	local language=$1 file=$2 text=$3 description=$4
	if ! active_source "$language" "$root/$file" | grep -F -- "$text" >/dev/null; then
		echo "mobile browser contract: $description" >&2
		exit 1
	fi
}

require_active_regex() {
	local language=$1 file=$2 pattern=$3 description=$4
	if ! active_source "$language" "$root/$file" | grep -E -- "$pattern" >/dev/null; then
		echo "mobile browser contract: $description" >&2
		exit 1
	fi
}

active_go_function() {
	local file=$1 function=$2
	active_source go "$file" | awk -v marker="func $function(" '
		index($0, marker) { in_function = 1 }
		in_function {
			print
			braces = $0
			opens += gsub(/\{/, "{", braces)
			closes += gsub(/\}/, "}", braces)
			if (opens > 0 && opens == closes) exit
		}
	'
}

require_active_go_function_text() {
	local file=$1 function=$2 text=$3 description=$4
	if ! active_go_function "$root/$file" "$function" | \
		grep -F -- "$text" >/dev/null; then
		echo "mobile browser contract: $description" >&2
		exit 1
	fi
}

reject_text() {
	local file=$1 pattern=$2 description=$3
	if grep -Eqi "$pattern" "$root/$file"; then
		echo "mobile browser contract: $description" >&2
		exit 1
	fi
}

dockerfile=Dockerfile.mobile-browser
entrypoint=scripts/mobile-browser-container.sh
runner=internal/handler/mobile_browser_runner_test.go
receipt=internal/handler/mobile_browser_receipt_test.go
strictness=internal/handler/mobile_browser_strictness_test.go
environment=internal/handler/mobile_browser_test.go
harness=scripts/mobile_readability_qa.mjs
ci=.gitlab-ci.yml

for file in "$dockerfile" "$entrypoint" "$runner" "$receipt" "$strictness" \
	"$environment" "$harness" Makefile "$ci"; do
	require_file "$file"
done

require_active_text docker "$dockerfile" 'mcr.microsoft.com/playwright:v1.61.1-noble' \
	'Playwright image pin is missing'
require_active_regex docker "$dockerfile" \
	'^[[:space:]]*RUN[[:space:]]+GOBIN=/usr/local/bin[[:space:]]+go[[:space:]]+install[[:space:]]+github\.com/a-h/templ/cmd/templ@v0\.3\.1020[[:space:]]*$' \
	'deterministic templ installation is missing'
require_active_regex docker "$dockerfile" '^[[:space:]]*RUN[[:space:]]+npm ci[[:space:]]*$' \
	'locked Node dependency installation is missing'
require_active_text docker "$dockerfile" 'ENTRYPOINT ["/usr/local/bin/mobile-browser-container"]' \
	'container entrypoint is missing'
require_text "$entrypoint" 'templ generate' \
	'container does not generate templ output before testing'
require_text "$entrypoint" \
	': "${MOBILE_BROWSER_NODE_MODULES:=/opt/mobile-browser/node_modules}"' \
	'container does not select its locked Node module path'
require_text "$entrypoint" 'export MOBILE_BROWSER_NODE_MODULES' \
	'container does not pass its Node module path to the tagged test'
require_text "$entrypoint" 'export NODE_PATH="$MOBILE_BROWSER_NODE_MODULES"' \
	'container does not align Node resolution with its selected module path'
require_text "$entrypoint" "go test -tags=mobilebrowser -run '^TestMobileBrowserReadability$'" \
	'container does not run the strict tagged browser test'
require_text "$entrypoint" '-count=1 -v ./internal/handler' \
	'container does not run the browser test once against the handler package'
require_text "$entrypoint" 'MOBILE_EVIDENCE_DIR="$evidence_dir"' \
	'container does not pass the artifact directory to the browser test'
require_text "$entrypoint" 'MOBILE_EVIDENCE_FILE="$receipt_name"' \
	'container does not name the durable receipt'
require_text "$entrypoint" 'trap copy_receipt EXIT' \
	'container does not copy the receipt on exit'

require_active_text go "$environment" '"MOBILE_STRICT=1"' \
	'normal browser test environment does not force strict mode'
require_active_text go "$strictness" 'normal browser test environment enables diagnostic baseline' \
	'strict-mode regression test is missing'
require_active_text js "$harness" \
	'process.env.MOBILE_BASELINE === "1" && process.env.MOBILE_STRICT !== "1"' \
	'baseline is not explicit diagnostic-only behavior'

require_active_text go "$runner" 'writeMobileBrowserReceipt(' \
	'Go test does not create a durable receipt'
require_active_go_function_text "$runner" requireMobileBrowserPrerequisites \
	'mobileBrowserNodeModules(root)' \
	'Go prerequisite does not use the selected Node module path'
require_active_go_function_text "$runner" requireMobileBrowserPrerequisites \
	'"NODE_PATH="+nodeModules' \
	'Go prerequisite does not align Node resolution with the selected module path'
require_active_go_function_text "$runner" mobileBrowserNodeModules \
	'filepath.Join(root, "node_modules")' \
	'Go prerequisite no longer defaults to host node_modules'
require_active_text js "$harness" \
	'process.env.MOBILE_BROWSER_NODE_MODULES || join(process.cwd(), "node_modules")' \
	'Playwright harness does not default to host node_modules'
require_active_text js "$harness" \
	'createRequire(join(nodeModules, "package.json"))' \
	'Playwright harness does not resolve from the selected Node module path'
require_active_text js "$harness" 'require("playwright")' \
	'Playwright harness does not require the selected module path'
require_active_go_function_text "$receipt" writeMobileBrowserReceipt \
	'os.MkdirAll(evidenceDir, 0o700)' \
	'Go receipt writer does not create the evidence directory securely'
require_active_go_function_text "$receipt" writeMobileBrowserReceipt \
	'os.CreateTemp(evidenceDir, ".receipt-")' \
	'Go receipt writer does not create the receipt atomically'
require_active_go_function_text "$receipt" writeMobileBrowserReceipt \
	'filepath.Join(evidenceDir, evidenceFile)' \
	'Go receipt writer does not place receipts under .omo/evidence'

require_text Makefile 'docker build -f Dockerfile.mobile-browser' \
	'Make does not build the shared browser image'
require_text Makefile 'mobile-browser-container' \
	'Make does not invoke the shared browser entrypoint'
require_text "$ci" 'docker build -f Dockerfile.mobile-browser' \
	'CI does not build the shared browser image'
require_text "$ci" 'mobile-browser-container' \
	'CI does not invoke the shared browser entrypoint'
require_text "$ci" 'alias: docker' 'CI Docker service alias is missing'
require_text "$ci" 'command: ["--tls=false"]' 'CI Docker TLS setting is missing'
require_text "$ci" 'DOCKER_HOST: tcp://docker:2375' 'CI Docker host is missing'
require_text "$ci" 'DOCKER_TLS_CERTDIR: ""' 'CI Docker TLS directory setting is missing'
require_text "$ci" 'for attempt in $(seq 1 30); do' 'CI Docker readiness loop is missing'
require_text "$ci" 'Docker daemon did not become ready after 30 seconds' \
	'CI Docker readiness diagnostic is missing'
require_text "$ci" 'docker info 2>&1 || true' 'CI Docker readiness diagnostics are missing'
require_text "$ci" 'if ! docker info >/tmp/mobile-browser-docker-info 2>&1; then' \
	'CI Docker after-script diagnostic is missing'
require_text "$ci" 'skipping failure diagnostics and container cleanup' \
	'CI Docker after-script diagnostic is missing'
require_text "$ci" 'docker container inspect "$MOBILE_BROWSER_CONTAINER"' \
	'CI container existence check is missing'
require_text "$ci" 'mobile:browser failure diagnostics (capped at 32 KiB):' \
	'CI failure diagnostic heading is missing'
require_text "$ci" 'mobile-readability-*.json' \
	'CI failure diagnostic receipt allowlist is missing'
require_text "$ci" 'head -c 32768' \
	'CI failure diagnostic byte cap is missing'
require_text "$ci" 'docker rm -f "$MOBILE_BROWSER_CONTAINER"' \
	'CI container cleanup is missing'

if sed -n '/^mobile:browser:/,/^auth:mfa-sqlite:/p' "$root/$ci" | \
	grep -Eq 'docker start -a.*\|\| true|docker run.*\|\| true'; then
	echo 'mobile browser contract: CI main test command must not be failure-masked' >&2
	exit 1
fi

require_active_text js "$harness" 'const executablePath = chromium.executablePath();' \
	'Playwright Chromium executable discovery is missing'
require_active_text go "$runner" 'Playwright Chromium prerequisite unavailable' \
	'strict Chromium prerequisite failure is missing'

for file in \
	internal/handler/mobile_browser_runner_test.go \
	internal/handler/mobile_browser_interrupt_test.go; do
	require_file "$file"
	reject_text "$file" 't\.Skip' "mobile browser prerequisite skips remain in $file"
done

for file in \
	"$harness" \
	internal/handler/mobile_browser_runner_test.go \
	internal/handler/mobile_browser_cleanup_test.go \
	internal/handler/mobile_browser_interrupt_test.go \
	"$entrypoint" \
	Makefile \
	docs/mobile-browser-ci.md; do
	require_file "$file"
	reject_text "$file" 'flatpak|MOBILE_BROWSER_MODE' \
		"legacy mobile browser mode remains in $file"
done

if sed -n '/^mobile:browser:/,/^auth:mfa-sqlite:/p' "$root/$ci" | \
	grep -Eq 'apt-get|npm ci|playwright install|templ generate'; then
	echo 'mobile browser contract: CI duplicates container provisioning' >&2
	exit 1
fi
