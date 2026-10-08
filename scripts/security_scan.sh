#!/usr/bin/env bash
set -euo pipefail
umask 077
export GOTOOLCHAIN=go1.26.8

root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
scanner=${1:?usage: security_scan.sh govulncheck|gosec|gitleaks [directory]}
scan_root=$(CDPATH= cd -- "${2:-$root}" && pwd)
tool_dir=${SECURITY_TOOL_DIR:-$root/bin/security}
report_dir=${SECURITY_REPORT_DIR:-$root/artifacts/security}
mkdir -p "$report_dir"
report_dir=$(CDPATH= cd -- "$report_dir" && pwd)
private=$(mktemp -d)
trap 'rm -rf "$private"' EXIT

fail() {
  printf '%s: %s\n' "$scanner" "$1" >&2
  exit 2
}

case "$scanner" in
  govulncheck) module=golang.org/x/vuln; version=v1.8.0 ;;
  gosec) module=github.com/securego/gosec/v2; version=v2.29.0 ;;
  gitleaks) module=github.com/zricethezav/gitleaks/v8; version=v8.30.1 ;;
  *) fail 'unknown scanner' ;;
esac
report="$report_dir/$scanner.json"
if [[ "$scanner" == govulncheck ]]; then rm -f "$report_dir/govulncheck.txt"; fi
printf '{"scanner":"%s","version":"%s","status":"incomplete"}\n' "$scanner" "$version" >"$report"
command -v jq >/dev/null || fail 'jq is required'
[[ -x "$tool_dir/$scanner" ]] || fail 'pinned tool missing; run make security-tools'
installed=$(go version -m "$tool_dir/$scanner" | awk '$1 == "mod" {print $2 "@" $3}')
[[ "$installed" == "$module@$version" ]] || fail 'tool version does not match the pin'
cd "$scan_root"
scan_exit=0
case "$scanner" in
  govulncheck)
    "$tool_dir/govulncheck" -db "${SECURITY_VULN_DB:-https://vuln.go.dev}" \
      -show traces,verbose,version ./... \
      >"$private/output" 2>"$private/errors" || scan_exit=$?
    # Text mode fails on reachable vulnerabilities; JSON/SARIF do not.
    [[ "$scan_exit" == 0 || "$scan_exit" == 3 ]] || fail 'scanner execution failed'
    grep -Eq 'Your code is affected by|^No vulnerabilities found\.$' "$private/output" \
      || fail 'missing scan summary'
    cp "$private/output" "$report_dir/govulncheck.txt"
    jq -n --arg version "$version" --argjson result "$scan_exit" \
      '{version: $version, status: "complete", exit_code: $result}' >"$report"
    ;;
  gosec)
    "$tool_dir/gosec" -nosec=true -tests=false -exclude-generated -fmt=json \
      -out="$private/raw.json" ./... >"$private/log" 2>&1 || scan_exit=$?
    [[ "$scan_exit" == 0 || "$scan_exit" == 1 ]] || fail 'scanner execution failed'
    jq -se --arg root "$scan_root" -f "$root/.security/gosec-report.jq" \
      "$private/raw.json" >"$private/safe.json" 2>"$private/parse-errors" \
      || fail 'incomplete or malformed report; no raw output published'
    while IFS= read -r finding; do
      file=$(jq -r '.file' <<<"$finding")
      [[ -f "$file" ]] || fail 'reported source file missing'
      digest=$(sha256sum -- "$file" | cut -d ' ' -f 1)
      jq --arg digest "$digest" '. + {source_sha256: $digest}' <<<"$finding"
    done < <(jq -c '.issues[]' "$private/safe.json") >"$private/issues.jsonl"
    jq -s --slurpfile exceptions "$root/.security/gosec-exceptions.json" \
      --arg today "$(date -u +%F)" --arg version "$version" '
      if ($exceptions | length) != 1 or ($exceptions[0] | type) != "array"
        or any($exceptions[0][];
          (.owner | type) != "string" or (.owner | length) == 0
          or (.expires | test("^[0-9]{4}-[0-9]{2}-[0-9]{2}$")) != true
          or (.source_sha256 | test("^[0-9a-f]{64}$")) != true
          or (.reason | type) != "string" or (.reason | length) == 0)
      then error("invalid exceptions") else . end
      |
      map(. as $finding | . + {disposition:
        if any($exceptions[0][];
          .rule == $finding.rule and .file == $finding.file
          and .line == $finding.line and .source_sha256 == $finding.source_sha256
          and .expires > $today and (.reason | length) > 0) then "reviewed"
        elif .severity == "LOW" then "advisory" else "blocking" end})
      | {version: $version, status: "complete", issues: .,
         blocking: ([.[] | select(.disposition == "blocking")] | length)}
    ' "$private/issues.jsonl" >"$private/report.json" 2>"$private/parse-errors" \
      || fail 'invalid exception registry'
    cp "$private/report.json" "$report"
    if jq -e '.blocking > 0' "$report" >/dev/null; then scan_exit=1; else scan_exit=0; fi
    ;;
  gitleaks)
    expires=$(sed -n 's/^# Exceptions expire: //p' "$root/.security/gitleaks.toml")
    [[ "$expires" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ && "$expires" > "$(date -u +%F)" ]] \
      || fail 'synthetic credential exceptions require renewed review'
    args=(--config "$root/.security/gitleaks.toml" --redact=100 --no-banner
      --no-color --exit-code=10 --report-format=json)
    if git rev-parse --show-toplevel >/dev/null 2>&1; then
      [[ "$(git rev-parse --show-toplevel)" == "$scan_root" ]] || fail 'scan must start at repository root'
      history_args=(--all)
      if [[ -n "${SECURITY_GIT_RANGE:-}" ]]; then
        [[ "$SECURITY_GIT_RANGE" =~ ^[0-9a-f]{40}\.\.[0-9a-f]{40}$ ]] || fail 'invalid PR commit range'
        history_args=("$SECURITY_GIT_RANGE")
      fi
      "$tool_dir/gitleaks" git "${args[@]}" --log-opts="${history_args[*]}" \
        --report-path="$private/history.json" >"$private/history.log" 2>&1 || scan_exit=$?
      [[ "$scan_exit" == 0 || "$scan_exit" == 10 ]] || fail 'history scan failed'
      mkdir "$private/tree"
      # Scan tracked files and new source, not ignored tools, DBs, or reports.
      git ls-files --cached --others --exclude-standard -z >"$private/files"
      [[ -s "$private/files" ]] || fail 'no source files to scan'
      tar -c --null -T "$private/files" | tar -x -C "$private/tree"
    else
      printf '[]\n' >"$private/history.json"
      ln -s "$scan_root" "$private/tree"
    fi
    tree_exit=0
    cd "$private/tree"
    "$tool_dir/gitleaks" dir "${args[@]}" --report-path="$private/tree.json" \
      . >"$private/tree.log" 2>&1 || tree_exit=$?
    [[ "$tree_exit" == 0 || "$tree_exit" == 10 ]] || fail 'tree scan failed'
    for kind in history tree; do
      jq -se -f "$root/.security/gitleaks-report.jq" "$private/$kind.json" \
        >"$private/$kind.safe.json" 2>"$private/parse-errors" \
        || fail 'incomplete or malformed report'
    done
    jq -s --arg version "$version" '
      {version: $version, status: "complete", findings: (.[0] + .[1])}
      | . + {blocking: (.findings | length)}
    ' "$private/history.safe.json" "$private/tree.safe.json" >"$private/report.json" \
      2>"$private/parse-errors" || fail 'incomplete or malformed report'
    cp "$private/report.json" "$report"
    if [[ "$scan_exit" == 10 || "$tree_exit" == 10 ]] \
      || jq -e '.blocking > 0' "$report" >/dev/null; then scan_exit=1; fi
    ;;
esac
printf '%s: complete (exit %s); report %s\n' "$scanner" "$scan_exit" "$report"
exit "$scan_exit"
