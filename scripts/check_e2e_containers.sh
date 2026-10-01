#!/usr/bin/env bash
# Assert cleanup without deleting evidence or touching another run's resources.
set -euo pipefail
namespace=${1:?namespace required}
since=${2:?suite start time required}
filter="label=io.durpdeploy.namespace=docker:$namespace"
remaining=$(docker ps -aq --filter "$filter")
if [[ -z "$remaining" ]]; then
	exit 0
fi

printf 'E2E namespace %s has surviving containers\n' "$namespace" >&2
while IFS= read -r id; do
	# Never dump full inspect: environment/command fields can contain secrets.
	docker inspect --format \
		'{{.Id}} {{.Name}} state={{.State.Status}} exit={{.State.ExitCode}} oom={{.State.OOMKilled}} created={{.Created}} started={{.State.StartedAt}} finished={{.State.FinishedAt}}' \
		"$id" >&2 || true
done <<<"$remaining"
docker events --since "$since" --until "$(date -u +%FT%TZ)" \
	--filter "$filter" \
	--filter event=create --filter event=start --filter event=die \
	--filter event=stop --filter event=kill --filter event=destroy \
	--format '{{.TimeNano}} {{.Action}} {{.Actor.ID}} {{index .Actor.Attributes "name"}}' \
	>&2 || true
exit 1
