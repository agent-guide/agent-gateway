#!/bin/sh

set -eu

project_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
env_file="$project_dir/.env.local"
project_name=${AGW_COMPOSE_PROJECT:-agent-gateway-first-run}
if [ ! -f "$env_file" ]; then
	echo "run ./setup.sh first" >&2
	exit 1
fi

if grep -q '^AGW_OTLP_ENDPOINT=' "$env_file"; then
	docker compose --project-name "$project_name" --project-directory "$project_dir" \
		--env-file "$env_file" -f "$project_dir/compose.yaml" \
		-f "$project_dir/compose.observability.yaml" --profile observability \
		run --rm smoke
else
	docker compose --project-name "$project_name" --project-directory "$project_dir" \
		--env-file "$env_file" -f "$project_dir/compose.yaml" run --rm smoke
fi
