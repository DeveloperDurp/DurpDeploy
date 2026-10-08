#!/usr/bin/env bash
set -euo pipefail

root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
test_dir=$(mktemp -d)
trap 'rm -rf -- "$test_dir"' EXIT
mkdir "$test_dir/bin"
export DEV_KEY_TEST_HASHES="$test_dir/hashes"
cat >"$test_dir/bin/go" <<'GO'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$*" == *air-verse* ]]; then
	printf '%s' "$DURPDEPLOY_SECRET_KEY" | sha256sum >>"$DEV_KEY_TEST_HASHES"
fi
GO
chmod 0755 "$test_dir/bin/go"
export PATH="$test_dir/bin:$PATH"
unset DURPDEPLOY_SECRET_KEY

run_dev() {
	make --no-print-directory -C "$root" dev-server \
		ENV_FILE="$test_dir/.env" >"$test_dir/output" 2>&1
}

run_dev
run_dev
[[ $(sort -u "$DEV_KEY_TEST_HASHES" | wc -l) -eq 1 ]]
key_file="$test_dir/.local/dev-secret-key"
[[ $(stat -c %a "$key_file") == 600 ]]
[[ $(base64 -d <"$key_file" | wc -c) -eq 32 ]]
if grep -Fq "$(cat "$key_file")" "$test_dir/output"; then
	printf 'FAIL: development key appeared in command output\n' >&2
	exit 1
fi
original_hash=$(sha256sum <"$key_file")

# Explicit shell and .env keys take precedence without replacing the fallback.
export DURPDEPLOY_SECRET_KEY=environment-test-key
run_dev
[[ $(tail -n 1 "$DEV_KEY_TEST_HASHES") == "$(printf %s environment-test-key | sha256sum)" ]]
printf 'DURPDEPLOY_SECRET_KEY=dotenv-test-key\n' >"$test_dir/.env"
run_dev
[[ $(tail -n 1 "$DEV_KEY_TEST_HASHES") == "$(printf %s dotenv-test-key | sha256sum)" ]]
[[ $(sha256sum <"$key_file") == "$original_hash" ]]

# A damaged key must fail startup, never silently generate a replacement.
unset DURPDEPLOY_SECRET_KEY
rm "$test_dir/.env"
: >"$key_file"
if run_dev; then
	printf 'FAIL: empty persisted key accepted\n' >&2
	exit 1
fi
[[ ! -s "$key_file" ]]
printf 'Development encryption key persistence: PASS\n'
