#!/usr/bin/env bash
set -euo pipefail

root=${AGENT_SYSTEMD_CONTRACT_ROOT:-.}
unit="$root/systemd/durpdeploy.service"
grep -Fq 'DURPDEPLOY_AGENT_LISTEN_ADDR=0.0.0.0:10943' "$unit"
grep -Fq 'DURPDEPLOY_AGENT_PUBLIC_URL=https://<agent-control-host>' "$unit"
grep -Fq 'DURPDEPLOY_AGENT_IDENTITY_DIR=/var/lib/durpdeploy/agent-identity' "$unit"
grep -Fq 'ReadWritePaths=/var/lib/durpdeploy /var/lib/durpdeploy/agent-identity' "$unit"
grep -Fq 'Environment=DURPDEPLOY_EXECUTION_BOUNDARY=service' "$unit"
grep -Fxq 'AmbientCapabilities=' "$unit"
grep -Fxq 'CapabilityBoundingSet=' "$unit"
grep -Fq 'NoNewPrivileges=true' "$unit"
grep -Fq 'ProtectSystem=strict' "$unit"
grep -Fq 'PrivateTmp=true' "$unit"
grep -Fq 'PrivateMounts=true' "$unit"
grep -Fq 'ProtectControlGroups=true' "$unit"
grep -Fq 'MemoryMax=512M' "$unit"
grep -Fq 'TasksMax=256' "$unit"
grep -Fq 'CPUQuota=100%' "$unit"
python3 - "$unit" <<'PY'
import pathlib
import sys

section = ""
for raw_line in pathlib.Path(sys.argv[1]).read_text().splitlines():
    line = raw_line.strip()
    if line.startswith("[") and line.endswith("]"):
        section = line[1:-1].casefold()
        continue
    if section != "service" or not line or line.startswith(("#", ";")):
        continue
    key, separator, value = line.partition("=")
    if not separator:
        continue
    key = key.strip().casefold()
    value = value.strip().casefold()
    if key in {"ambientcapabilities", "capabilityboundingset"} and value:
        raise SystemExit("agent systemd contract: forbidden capability grant")
    if key == "delegate" and value == "true":
        raise SystemExit("agent systemd contract: forbidden Delegate=true")
    if key == "protectcontrolgroups" and value != "true":
        raise SystemExit(
            "agent systemd contract: forbidden ProtectControlGroups=false"
        )
    if key == "restrictnamespaces" and value != "true":
        raise SystemExit(
            "agent systemd contract: forbidden RestrictNamespaces=false"
        )
    if key == "bindreadonlypaths" and "/data" in value:
        raise SystemExit(
            "agent systemd contract: forbidden BindReadOnlyPaths=/data"
        )
    if key in {"bindpaths", "bindreadonlypaths"}:
        for socket in ("docker.sock", "podman.sock"):
            if socket in value:
                raise SystemExit(f"agent systemd contract: forbidden {socket}")
    if "chroot" in line.casefold():
        raise SystemExit("agent systemd contract: forbidden chroot")
    if "bind-mount" in line.casefold():
        raise SystemExit("agent systemd contract: forbidden bind-mount")
PY
printf '%s\n' 'agent systemd contract: PASS'
