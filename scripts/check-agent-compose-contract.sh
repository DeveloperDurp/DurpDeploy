#!/usr/bin/env bash
set -euo pipefail

root=${AGENT_COMPOSE_CONTRACT_ROOT:-.}
workdir=$(mktemp -d)
trap 'rm -rf "$workdir"' EXIT

if docker compose version >/dev/null 2>&1; then
	compose=(docker compose)
	compose_format=json
elif podman compose version >/dev/null 2>&1; then
	compose=(podman compose)
	compose_format=yaml
else
	echo 'agent compose contract: Docker Compose or Podman Compose is required' >&2
	exit 1
fi

for file in compose.yml compose.example.yml; do
	name=$(basename "$file" .yml)
	mkdir -p "$workdir/$name/secrets"
	cp "$root/$file" "$workdir/$name/compose.yml"
	: > "$workdir/$name/compose.app.env"
	: > "$workdir/$name/compose.caddy.env"
	: > "$workdir/$name/compose.litestream.env"
	: > "$workdir/$name/compose.agent.env"
	printf '%s\n' placeholder > "$workdir/$name/secrets/durpdeploy_key"
	"${compose[@]}" -f "$workdir/$name/compose.yml" config >/dev/null
	if [ "$compose_format" = json ]; then
		"${compose[@]}" -f "$workdir/$name/compose.yml" --profile agent config \
			--format json > "$workdir/$name/agent.json"
	else
		COMPOSE_PROFILES=agent "${compose[@]}" -f "$workdir/$name/compose.yml" config \
			> "$workdir/$name/agent.yml"
	fi
done

python3 - "$workdir" <<'PY'
import json
import pathlib
import sys

import yaml

root = pathlib.Path(sys.argv[1])
for path in list(root.glob("*/agent.json")) + list(root.glob("*/agent.yml")):
    document = json.loads(path.read_text()) if path.suffix == ".json" else yaml.safe_load(path.read_text())
    services = document["services"]
    agent = services["agent"]
    assert agent["image"] == "durpdeploy-agent:latest"
    assert agent["cap_drop"] == ["ALL"]
    assert agent["read_only"] is True
    assert agent["security_opt"] == ["no-new-privileges:true"]
    assert "network_mode" not in agent
    assert len(agent["volumes"]) == 1
    volume = agent["volumes"][0]
    if isinstance(volume, str):
        source, target, *_ = volume.split(":")
        volume = {"source": source, "target": target, "type": "volume"}
    assert volume["target"] == "/var/lib/durpdeploy-agent"
    assert volume["type"] == "volume"
    assert volume["source"].endswith("durpdeploy-agent-state")
    agent_text = json.dumps(agent)
    for forbidden in ("/data", "durpdeploy_key", "docker.sock", "privileged", "host"):
        assert forbidden not in agent_text, f"{path}: found forbidden {forbidden}"
print("agent compose contract: PASS")
PY
