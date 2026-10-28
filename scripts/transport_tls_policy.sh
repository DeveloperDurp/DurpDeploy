#!/usr/bin/env bash
# Raw scanner strings remain private. Emit only fixed policy check names.
set -euo pipefail
[[ $# == 1 && -s $1 ]] || { echo 'TLS report missing' >&2; exit 2; }
root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
if ! jq -se --slurpfile policy "$root/.security/transport-policy.json" '
    def one($rows; $id): [$rows[] | select(.id==$id)] |
        if length==1 then .[0] else {} end;
    def protocol($s; $id; $finding):
        one($s.protocols; $id) | (.finding==$finding);
    def certok($s; $id): one($s.serverDefaults; $id) | .severity=="OK";
    if length==1 then .[0] else error("expected one scanner report") end |
    .scanResult[0] as $s | $policy[0] as $p |
    {
      scanner_complete: ((.version|gsub("^ +| +$";""))==$p.testssl_version and
          (.scanResult|length)==1 and
          ([.clientProblem1[]? | select(.id!="engine_problem" or
            .finding!="No engine or GOST support via engine with your /usr/bin/openssl")]|length)==0),
      protocols: (protocol($s;"SSLv2";"not offered") and
          protocol($s;"SSLv3";"not offered") and protocol($s;"TLS1";"not offered") and
          protocol($s;"TLS1_1";"not offered") and protocol($s;"TLS1_2";"offered") and
          protocol($s;"TLS1_3";"offered with final")),
      ciphers: (($s.cipherTests|map(select(.id|startswith("cipher-")))) as $cs |
          ([$cs[]|select(.id|startswith("cipher-tls1_2_"))]|length)==3 and
          ([$cs[]|select(.id|startswith("cipher-tls1_3_"))]|length)==3 and
          ($cs|map(.id)|unique|length)==6 and
          all($cs[]; .severity=="OK" and (.id as $id|($p.tls12+$p.tls13)|index($id)!=null))),
      certificates: (certok($s;"cert_trust") and certok($s;"cert_chain_of_trust") and
          certok($s;"cert_signatureAlgorithm") and certok($s;"cert_extlifeSpan") and
          (one($s.serverDefaults;"cert_keySize").finding|test("^(RSA 2048 bits|EC 256 bits)")) and
          (one($s.serverDefaults;"cert_extKeyUsage").finding=="TLS Web Server Authentication") and
          (one($s.serverDefaults;"cert_expirationStatus").finding|test("^expires < 30 days \\([1-7]\\)$")) and
          (one($s.serverDefaults;"cert_revocation").finding=="Neither CRL nor OCSP URI provided"))
    } | .passed=(.scanner_complete and .protocols and .ciphers and .certificates) |
    .fixture_exceptions=["private-ca-without-revocation","short-lived-valid-certificate"] |
    .tool_limitations=["no-GOST-engine-for-disallowed-ciphers"] |
    ., (if .passed then empty else error("TLS policy failed") end)
' "$1" 2>/dev/null; then
    echo 'TLS policy failed or scanner evidence incomplete' >&2
    exit 1
fi
