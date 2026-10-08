#!/usr/bin/env bash
# Sourced by dev-server; keep the key out of command output.
if [ -f "$DURPDEPLOY_ENV_FILE" ]; then . "$DURPDEPLOY_ENV_FILE"; fi
if [ -z "${DURPDEPLOY_SECRET_KEY:-}" ] && [ ! -f /etc/durpdeploy/key ]; then
	dev_key_file="$(dirname "$DURPDEPLOY_ENV_FILE")/.local/dev-secret-key"
	if [ ! -f "$dev_key_file" ]; then
		(
			umask 077
			mkdir -p "$(dirname "$dev_key_file")" || exit 1
			dev_key_temp=$(mktemp "$dev_key_file.XXXXXX") || exit 1
			trap 'rm -f "$dev_key_temp"' EXIT
			openssl rand -base64 32 >"$dev_key_temp" || exit 1
			# Publish once, without replacing a key from another startup.
			ln "$dev_key_temp" "$dev_key_file" 2>/dev/null ||
				[ -f "$dev_key_file" ]
		) || return 1
	fi
	DURPDEPLOY_SECRET_KEY=$(cat "$dev_key_file") || return 1
	if [ -z "$DURPDEPLOY_SECRET_KEY" ]; then
		printf 'ERROR: development encryption key is empty: %s\n' "$dev_key_file" >&2
		return 1
	fi
fi
export DURPDEPLOY_SECRET_KEY
