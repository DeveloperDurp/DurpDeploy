#!/usr/bin/env bash
set -euo pipefail
root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
runner="$root/scripts/container_scan.sh"
expect_exit() {
  local expected=$1 actual=0
  shift
  "$@" >"$work/output" 2>&1 || actual=$?
  if [[ "$actual" != "$expected" ]]; then
    printf 'Expected exit %s, got %s\n' "$expected" "$actual" >&2
    cat "$work/output" >&2
    exit 1
  fi
}

# Actual Trivy must detect an unsafe Dockerfile and rendered workload.
mkdir -p "$work/source/charts"
cp -R "$root/charts/durpdeploy" "$work/source/charts/"
sed '/allowPrivilegeEscalation: false/a\  privileged: true' \
  "$work/source/charts/durpdeploy/values.yaml" >"$work/unsafe-values.yaml"
cp "$work/unsafe-values.yaml" "$work/source/charts/durpdeploy/values.yaml"
printf 'FROM alpine:3.20\nUSER root\n' >"$work/source/Dockerfile"
expect_exit 1 bash "$runner" config "$work/source" "$work/config"
jq -e '.status == "complete" and .blocking > 0 and
  any(.results[]; .type == "dockerfile" and any(.misconfigurations[]; .id == "DS-0002" and .status == "FAIL")) and
  any(.results[]; .type == "kubernetes" and any(.misconfigurations[];
    (.severity == "HIGH" or .severity == "CRITICAL") and .status == "FAIL"))' \
  "$work/config/scan.json" >/dev/null

# A harmless old dependency is analyzed, never installed or executed.
printf '{"name":"security-fixture","version":"1.0.0","dependencies":{"lodash":"4.17.20"}}\n' >"$work/source/package.json"
cat >"$work/source/package-lock.json" <<'JSON'
{"name":"security-fixture","version":"1.0.0","lockfileVersion":3,"packages":{"":{"name":"security-fixture","version":"1.0.0","dependencies":{"lodash":"4.17.20"}},"node_modules/lodash":{"version":"4.17.20","resolved":"https://registry.npmjs.org/lodash/-/lodash-4.17.20.tgz"}}}
JSON
expect_exit 1 bash "$runner" dependencies "$work/source" "$work/dependencies"
jq -e '.blocking > 0 and any(.results[].vulnerabilities[]; .package == "lodash")' \
  "$work/dependencies/scan.json" >/dev/null
# Development dependencies include build tools and Swagger UI assets bundled in production.
jq '.devDependencies=.dependencies | del(.dependencies)' "$work/source/package.json" >"$work/package.json"
cp "$work/package.json" "$work/source/package.json"
jq '.packages[""].devDependencies=.packages[""].dependencies | del(.packages[""].dependencies) |
  .packages["node_modules/lodash"].dev=true' "$work/source/package-lock.json" >"$work/package-lock.json"
cp "$work/package-lock.json" "$work/source/package-lock.json"
expect_exit 1 bash "$runner" dependencies "$work/source" "$work/dev-dependencies"
jq -e '.blocking > 0 and any(.results[].vulnerabilities[]; .package == "lodash")' \
  "$work/dev-dependencies/scan.json" >/dev/null
# An inline suppression cannot convert an unsafe configuration to a pass.
printf '# trivy:ignore:DS0002\nFROM alpine:3.20\nUSER root\n' >"$work/source/Dockerfile"
expect_exit 2 bash "$runner" config "$work/source" "$work/config"
jq -e '.status == "incomplete"' "$work/config/scan.json" >/dev/null

# The policy independently proves severity/fix behavior and strips raw content.
canary="synthetic-private-$(openssl rand -hex 16)"
jq -n --arg canary "$canary" '{SchemaVersion:2,Results:[{Target:"package-lock.json",Type:"npm",
  Vulnerabilities:[{VulnerabilityID:"CVE-2099-0001",PkgName:"fixture",InstalledVersion:"1",FixedVersion:"2",Severity:"HIGH",Description:$canary}],
  Misconfigurations:[], Secrets:[{Match:$canary}]}],Metadata:{Env:[$canary]}}' >"$work/raw.json"
jq -se -f "$root/.security/trivy-report.jq" "$work/raw.json" >"$work/safe.json"
jq -e '.blocking == 1' "$work/safe.json" >/dev/null
for mutation in '.Results[0].Vulnerabilities[0].FixedVersion=""' '.Results[0].Vulnerabilities[0].Severity="MEDIUM"'; do
  jq "$mutation" "$work/raw.json" | jq -se -f "$root/.security/trivy-report.jq" >"$work/advisory.json"
  jq -e '.blocking == 0 and (.results[0].vulnerabilities | length) == 1' "$work/advisory.json" >/dev/null
done
for mutation in '.SchemaVersion=0' '.Results=[]' '.Results[0].Vulnerabilities[0].Severity="invalid"' \
  '.Results[0].Vulnerabilities[0].FixedVersion=123' '.Results[0].Target=null'; do
  jq "$mutation" "$work/raw.json" >"$work/bad.json"
  expect_exit 5 jq -se -f "$root/.security/trivy-report.jq" "$work/bad.json"
done
if grep -Fq "$canary" "$work/safe.json"; then exit 1; fi

# Exercise the complete image/SBOM/report path without building or running code.
# Fault injection simulates tool/database/report failures, not scan detection.
mkdir -p "$work/tool" "$work/layout"
printf '{"schemaVersion":2,"manifests":[{"digest":"sha256:%064d"}]}\n' 0 >"$work/layout/index.json"
cat >"$work/tool/trivy" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
directory=$(dirname "$0")
if [[ " $* " == *' --version '* ]]; then cat "$directory/version.json"; exit 0; fi
if [[ -f "$directory/fail" ]]; then printf 'private scanner error\n' >&2; exit 2; fi
output=''
convert=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    --output) output=$2; shift ;;
    convert) convert=true ;;
  esac
  shift
done
if "$convert"; then cp "$directory/sbom.json" "$output"; else cp "$directory/raw.json" "$output"; fi
SH
chmod +x "$work/tool/trivy"
printf '{"Version":"0.76.0","VulnerabilityDB":{"Version":2,"UpdatedAt":"2026-10-01T00:00:00Z"}}\n' >"$work/tool/version.json"
jq '.Results[0].Packages=[{Name:"fixture",Version:"1"}] | .Results[0].Type="gobinary" |
  .Results += [{Target:"alpine",Type:"alpine",Class:"os-pkgs",Packages:[{Name:"alpine",Version:"1"}]}]' \
  "$work/raw.json" >"$work/tool/raw.json"
jq -n --arg canary "$canary" '{bomFormat:"CycloneDX",specVersion:"1.6",version:1,
  components:[{type:"library",name:"fixture",version:"1","bom-ref":"fixture",properties:[{name:"private",value:$canary}]}],
  metadata:{component:{type:"container",name:"image","bom-ref":"image",properties:[{name:"private",value:$canary}]},
    properties:[{name:"private",value:$canary}]},dependencies:[{ref:"image",dependsOn:["fixture"]},{ref:"fixture",dependsOn:[]}]}' >"$work/tool/sbom.json"
expect_exit 1 env TRIVY_TOOL_DIR="$work/tool" bash "$runner" image "$work/layout" "$work/image"
jq -e '.status == "complete" and .blocking == 1 and (.image_manifest_digests | length) == 1' "$work/image/scan.json" >/dev/null
jq -e '.bomFormat == "CycloneDX" and (.components | length) == 1 and
  (.metadata.component | has("properties") | not) and .dependencies[0].ref == .metadata.component["bom-ref"]' \
  "$work/image/sbom.cdx.json" >/dev/null
if grep -FRq "$canary" "$work/image" "$work/output"; then exit 1; fi
# No findings: same inventory still produces an SBOM and succeeds.
jq '.Results[0].Vulnerabilities=[]' "$work/tool/raw.json" >"$work/clean.json"
cp "$work/clean.json" "$work/tool/raw.json"
expect_exit 0 env TRIVY_TOOL_DIR="$work/tool" bash "$runner" image "$work/layout" "$work/image"
for fault in tool database malformed inventory coverage sbom; do
  cp "$work/clean.json" "$work/tool/raw.json"
  cp "$work/tool/version.json" "$work/version-backup.json"
  cp "$work/tool/sbom.json" "$work/sbom-backup.json"
  case "$fault" in
    tool) touch "$work/tool/fail" ;;
    database) printf '{"Version":"0.76.0"}\n' >"$work/tool/version.json" ;;
    malformed) printf '{' >"$work/tool/raw.json" ;;
    inventory) jq '.Results[0].Packages=[]' "$work/clean.json" >"$work/tool/raw.json" ;;
    coverage) jq '.Results[0].Type="unknown"' "$work/clean.json" >"$work/tool/raw.json" ;;
    sbom) printf '{}' >"$work/tool/sbom.json" ;;
  esac
  expect_exit 2 env TRIVY_TOOL_DIR="$work/tool" bash "$runner" image "$work/layout" "$work/image"
  jq -e '.status == "incomplete"' "$work/image/scan.json" >/dev/null
  [[ ! -f "$work/image/sbom.cdx.json" ]]
  rm -f "$work/tool/fail"
  mv "$work/version-backup.json" "$work/tool/version.json"
  mv "$work/sbom-backup.json" "$work/tool/sbom.json"
done
expect_exit 2 env TRIVY_TOOL_DIR="$work/missing" bash "$runner" image "$work/layout" "$work/image"
printf 'Container scan detection, policy, error, SBOM, and redaction fixtures: PASS\n'
