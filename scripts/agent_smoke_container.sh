#!/usr/bin/env bash
set -euo pipefail

export NODE_PATH=/opt/mobile-browser/node_modules
if [[ ! -e node_modules && ! -L node_modules ]]; then
	ln -s /opt/mobile-browser/node_modules node_modules
fi
templ generate
mkdir -p static/swagger-ui
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

exec make agent-smoke-test
