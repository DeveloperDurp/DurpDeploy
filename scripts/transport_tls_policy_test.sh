#!/usr/bin/env bash
set -euo pipefail
root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
private=$(mktemp -d)
trap 'rm -rf -- "$private"' EXIT
jq -n '{version:"3.2.4",scanResult:[{
 protocols:((["SSLv2","SSLv3","TLS1","TLS1_1"]|map({id:.,finding:"not offered"}))+
   [{id:"TLS1_2",finding:"offered"},{id:"TLS1_3",finding:"offered with final"}]),
 cipherTests:(["cipher-tls1_2_xc030","cipher-tls1_2_xc02f","cipher-tls1_2_xcca8",
   "cipher-tls1_3_x1301","cipher-tls1_3_x1302","cipher-tls1_3_x1303"]|map({id:.,severity:"OK"})),
 serverDefaults:((["cert_trust","cert_chain_of_trust","cert_signatureAlgorithm","cert_extlifeSpan"]|
   map({id:.,severity:"OK"}))+
   [{id:"cert_keySize",finding:"RSA 2048 bits"},
    {id:"cert_extKeyUsage",finding:"TLS Web Server Authentication"},
    {id:"cert_expirationStatus",finding:"expires < 30 days (6)"},
    {id:"cert_revocation",finding:"Neither CRL nor OCSP URI provided"}])
}]}' >"$private/valid.json"
bash "$root/scripts/transport_tls_policy.sh" "$private/valid.json" >"$private/report.json"
jq -e .passed "$private/report.json" >/dev/null
for mutation in \
    '.version="unknown"' \
    '.scanResult=[]' \
    '.scanResult[0].protocols[3].finding="offered"' \
    '.scanResult[0].cipherTests[0].id="cipher-tls1_2_private-canary-CBC"' \
    '.scanResult[0].cipherTests=[]' \
    '.scanResult[0].serverDefaults[0].severity="HIGH"' \
    '.scanResult[0].serverDefaults[6].finding="expired"' \
    '.clientProblem1=[{id:"private-canary",finding:"private-canary"}]'; do
    jq "$mutation" "$private/valid.json" >"$private/invalid.json"
    if bash "$root/scripts/transport_tls_policy.sh" "$private/invalid.json" \
        >"$private/report.json" 2>"$private/errors"; then
        echo 'TLS policy accepted incomplete or unsafe evidence' >&2; exit 1
    fi
    if grep -Fq private-canary "$private/report.json" "$private/errors"; then
        echo 'TLS policy leaked untrusted scanner data' >&2; exit 1
    fi
done
cat "$private/valid.json" "$private/valid.json" >"$private/stream.json"
if bash "$root/scripts/transport_tls_policy.sh" "$private/stream.json" \
    >"$private/report.json" 2>"$private/errors"; then
    echo 'TLS policy accepted multiple scanner reports' >&2; exit 1
fi
printf '{"private-canary":' >"$private/broken.json"
if bash "$root/scripts/transport_tls_policy.sh" "$private/broken.json" \
    >"$private/report.json" 2>"$private/errors"; then
    echo 'TLS policy accepted a truncated report' >&2; exit 1
fi
if grep -Fq private-canary "$private/report.json" "$private/errors"; then
    echo 'TLS parser exposed raw malformed data' >&2; exit 1
fi
if bash "$root/scripts/transport_tls_policy.sh" "$private/missing" >/dev/null 2>&1; then
    echo 'TLS policy accepted absent scanner evidence' >&2; exit 1
fi
echo 'TLS policy detection and redaction controls: PASS'
