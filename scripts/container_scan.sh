#!/usr/bin/env bash
set -euo pipefail
trap 'exit 2' ERR
umask 077
root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
mode=${1:?usage: container_scan.sh image|config|dependencies INPUT REPORT_DIRECTORY}
input=$(realpath "${2:?missing input}")
report_dir=${3:?missing report directory}
mkdir -p "$report_dir"
report_dir=$(realpath "$report_dir")
private=$(mktemp -d)
trap 'rm -rf "$private"' EXIT
report="$report_dir/scan.json"
rm -f "$report_dir/sbom.cdx.json"
printf '{"status":"incomplete"}\n' >"$report"
fail() { printf 'Trivy %s: %s\n' "$mode" "$1" >&2; exit 2; }
trivy=${TRIVY_TOOL_DIR:-$root/bin/security}/trivy
[[ -x "$trivy" ]] || fail 'missing pinned tool; run make trivy-tools'
# A clean environment prevents checkout settings and inherited TRIVY_* variables
# from disabling scanners or changing policy. Only a dedicated cache is retained.
cache=${TRIVY_SCAN_CACHE:-$root/bin/trivy-cache}
mkdir -p "$cache"
cache=$(realpath "$cache")
run_trivy() {
  env -i PATH="$PATH" HOME="$private" TMPDIR="$private" \
    "$trivy" --config /dev/null --cache-dir "$cache" "$@"
}
run_trivy --version --format json >"$private/version.json" 2>"$private/errors" || fail 'version query failed'
jq -e '.Version == "0.76.0"' "$private/version.json" >/dev/null || fail 'tool version does not match pin'
args=(--quiet --timeout 10m --ignorefile /dev/null --format json --output "$private/raw.json" --exit-code 0)
case "$mode" in
  image)
    [[ -f "$input/index.json" ]] || fail 'expected exported OCI layout'
    jq -e '.schemaVersion == 2 and (.manifests | length > 0) and
      all(.manifests[]; .digest | test("^sha256:[0-9a-f]{64}$"))' "$input/index.json" >/dev/null \
      || fail 'invalid OCI index'
    run_trivy image "${args[@]}" --scanners vuln --list-all-pkgs --input "$input" \
      >"$private/log" 2>&1 || fail 'image scan failed'
    ;;
  config)
    mkdir "$private/config"
    if grep -Rqi 'trivy:ignore' "$input/Dockerfile" "$input/charts/durpdeploy"; then
      fail 'inline suppressions require maintainer review; none are enabled'
    fi
    cp "$input/Dockerfile" "$private/config/Dockerfile"
    # Render only synthetic defaults with external Secret references.
    helm template security "$input/charts/durpdeploy" \
      --set secretKey.existingSecret=security-fixture \
      --set postgres.existingSecret=security-fixture >"$private/config/kubernetes.yaml" \
      2>"$private/errors" || fail 'Helm rendering failed'
    run_trivy config "${args[@]}" --include-non-failures --misconfig-scanners dockerfile,kubernetes \
      "$private/config" >"$private/log" 2>&1 || fail 'configuration scan failed'
    ;;
  dependencies)
    mkdir "$private/dependencies"
    cp "$input/package.json" "$input/package-lock.json" "$private/dependencies/"
    run_trivy fs "${args[@]}" --scanners vuln --pkg-types library --include-dev-deps "$private/dependencies" \
      >"$private/log" 2>&1 || fail 'dependency scan failed'
    ;;
  *) fail 'unknown scan mode' ;;
esac
jq -se -f "$root/.security/trivy-report.jq" "$private/raw.json" >"$private/safe.json" \
  2>"$private/errors" || fail 'incomplete or malformed report'
if [[ "$mode" == config ]]; then
  jq -e '[.results[].type] | index("dockerfile") != null and index("kubernetes") != null' \
    "$private/safe.json" >/dev/null || fail 'Dockerfile or Kubernetes coverage missing'
  jq -e 'all(.results[]; .misconfigurations | length > 0)' "$private/safe.json" >/dev/null \
    || fail 'configuration check coverage missing'
  run_trivy --version --format json >"$private/version.json" 2>"$private/errors" || fail 'check metadata missing'
  jq -e '.CheckBundle.Digest | test("^sha256:[0-9a-f]{64}$")' "$private/version.json" >/dev/null \
    || fail 'check metadata missing'
else
  if [[ "$mode" == dependencies ]]; then
    jq -e 'any(.results[]; .type == "npm")' "$private/safe.json" >/dev/null \
      || fail 'frontend dependency coverage missing'
  fi
  jq -e 'any(.Results[]; (.Packages | length) > 0)' "$private/raw.json" >/dev/null \
    || fail 'package inventory missing'
  if [[ "$mode" == image ]]; then
    jq -e 'any(.Results[]; .Type == "gobinary" and (.Packages | length) > 0) and
      any(.Results[]; .Class == "os-pkgs" and (.Packages | length) > 0)' \
      "$private/raw.json" >/dev/null || fail 'production OS or Go binary inventory missing'
  fi
  run_trivy --version --format json >"$private/version.json" 2>"$private/errors" || fail 'database metadata missing'
  jq -e '.VulnerabilityDB | .Version == 2 and (.UpdatedAt | type) == "string"' \
    "$private/version.json" >/dev/null || fail 'database metadata missing'
fi
if [[ "$mode" == image ]]; then
  run_trivy convert --format cyclonedx --output "$private/sbom.json" "$private/raw.json" \
    >"$private/log" 2>&1 || fail 'SBOM generation failed'
  # Keep the CycloneDX graph and package identifiers, without file properties or
  # image configuration (which can contain environment values).
  jq -e 'def component: {type, "bom-ref": .["bom-ref"], group, name, version, purl}
      | with_entries(select(.value != null));
    if .bomFormat != "CycloneDX" or (.components | type) != "array" or
    (.components | length) == 0 then error("empty SBOM") else
    {bomFormat, specVersion, version, components: [.components[] | component],
     dependencies: [(.dependencies // [])[] | {ref, dependsOn: (.dependsOn // [])}]} +
    (if .metadata.component then {metadata: {component: (.metadata.component | component)}} else {} end)
    end' "$private/sbom.json" >"$private/safe-sbom.json" \
    2>"$private/errors" || fail 'invalid SBOM'
  cp "$private/safe-sbom.json" "$report_dir/sbom.cdx.json"
  jq '[.manifests[].digest]' "$input/index.json" >"$private/digests.json"
else
  printf '[]\n' >"$private/digests.json"
fi
jq -n --slurpfile scan "$private/safe.json" --slurpfile tool "$private/version.json" \
  --slurpfile digests "$private/digests.json" --arg mode "$mode" \
  --arg commit "$(git -C "$root" rev-parse HEAD)" '
  $scan[0] + {status: "complete", mode: $mode, commit: $commit,
    image_manifest_digests: $digests[0], scanner: $tool[0].Version,
    database: ($tool[0].VulnerabilityDB // null | if . == null then null else
      {Version, UpdatedAt, NextUpdate, DownloadedAt} end),
    checks: ($tool[0].CheckBundle // null | if . == null then null else {Digest, DownloadedAt} end)}' >"$private/report.json"
cp "$private/report.json" "$report"
printf 'Trivy %s: complete; report %s\n' "$mode" "$report"
if jq -e '.blocking > 0' "$report" >/dev/null; then exit 1; fi
