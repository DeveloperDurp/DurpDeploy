#!/usr/bin/env bash
set -euo pipefail

root=${MFA_DOCUMENTATION_CONTRACT_ROOT:-.}
files=(
	README.md
	docs/deploy.md
	docs/security.md
	docs/roles.md
	docs/attack-drill.md
	compose.example.yml
	systemd/durpdeploy.service
)

require_text() {
	local file=$1 text=$2 description=$3
	if ! grep -Fq -- "$text" "$root/$file"; then
		echo "MFA documentation contract: $description" >&2
		exit 1
	fi
}

reject_pattern() {
	local pattern=$1 description=$2
	if grep -Eqi -- "$pattern" "${files[@]/#/$root/}"; then
		echo "MFA documentation contract: $description" >&2
		exit 1
	fi
}

for file in "${files[@]}"; do
	if [ ! -f "$root/$file" ]; then
		echo "MFA documentation contract: missing $file" >&2
		exit 1
	fi
done

require_text docs/deploy.md 'DURPDEPLOY_URL=https://durpdeploy.example.com' \
	'DURPDEPLOY_URL example is missing'
require_text docs/deploy.md 'hostname or origin invalidates existing' \
	'passkey RP identity warning is missing'
require_text docs/security.md 'Browser MFA is optional.' \
	'optional browser MFA boundary is missing'
require_text docs/security.md 'API tokens are single bearer' \
	'API token MFA boundary is missing'
require_text docs/roles.md 'viewer may manage only their own Security settings' \
	'viewer self-security exception is missing'
require_text docs/attack-drill.md 'MFA reset does not revoke API tokens.' \
	'admin-reset API token survival is missing'
require_text README.md 'Browser MFA protects browser sessions only.' \
	'README browser/API MFA boundary is missing'
require_text compose.example.yml 'DURPDEPLOY_URL: ${DURPDEPLOY_URL:?}' \
	'Compose does not pass DURPDEPLOY_URL to the app'
require_text systemd/durpdeploy.service \
	'Environment=DURPDEPLOY_URL=https://durpdeploy.example.com' \
	'systemd DURPDEPLOY_URL example is missing'

reject_pattern 'DURPDEPLOY_PUBLIC_URL' 'legacy public URL variable is documented'
reject_pattern 'otpauth://|totp[[:space:]_-]*(seed|secret)[[:space:]]*[:=]|recovery[[:space:]_-]*code[[:space:]]*[:=]' \
	'plaintext MFA secret example is documented'
reject_pattern 'totp.*phishing[- ]resistant|phishing[- ]resistant.*totp' \
	'TOTP phishing-resistance claim is documented'
reject_pattern 'api[[:space:]]+tokens?.*(require|are|remain).{0,30}mfa|mfa.{0,30}api[[:space:]]+tokens?.*(protect|require)' \
	'incorrect API-token MFA claim is documented'
