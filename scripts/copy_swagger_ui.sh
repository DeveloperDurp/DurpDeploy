#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 2 ]]; then
	echo "Usage: $0 SOURCE_DIR DESTINATION_DIR" >&2
	exit 2
fi

source_dir=$1
destination_dir=$2
mkdir -p "$destination_dir"
for asset in swagger-ui-bundle.js swagger-ui-standalone-preset.js \
	swagger-ui.css favicon-32x32.png favicon-16x16.png; do
	cp -- "$source_dir/$asset" "$destination_dir/"
done
