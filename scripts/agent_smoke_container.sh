#!/usr/bin/env bash
set -euo pipefail

export NODE_PATH=/opt/mobile-browser/node_modules
if [[ ! -e node_modules && ! -L node_modules ]]; then
	ln -s /opt/mobile-browser/node_modules node_modules
fi
templ generate
bash scripts/copy_swagger_ui.sh \
	/opt/mobile-browser/node_modules/swagger-ui-dist static/swagger-ui

exec make agent-smoke-test
