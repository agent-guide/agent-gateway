#!/bin/sh

set -eu

project_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
env_file="$project_dir/.env.local"
password_file="$project_dir/.admin-password"
project_name=${AGW_COMPOSE_PROJECT:-agent-gateway-first-run}
if [ ! -f "$env_file" ]; then
	echo "run ./setup.sh first" >&2
	exit 1
fi

auth=$(sed -n "s/^AGW_ADMIN_BASIC_AUTH='\(.*\)'$/\1/p" "$env_file")
case "$auth" in
	admin:*) ;;
	*)
		echo "cannot read the setup-generated Admin credential" >&2
		exit 1
		;;
esac
password=${auth#admin:}
umask 077
printf '%s' "$password" >"$password_file"

next_env="$env_file.next"
grep -v '^AGW_OTLP_ENDPOINT=' "$env_file" >"$next_env" || true
printf "AGW_OTLP_ENDPOINT='127.0.0.1:4317'\n" >>"$next_env"
mv "$next_env" "$env_file"

compose() {
	docker compose --project-name "$project_name" --project-directory "$project_dir" \
		--env-file "$env_file" -f "$project_dir/compose.yaml" \
		-f "$project_dir/compose.observability.yaml" \
		--profile observability "$@"
}

attempt=0
until compose pull tempo otel-collector prometheus grafana; do
	attempt=$((attempt + 1))
	if [ "$attempt" -ge 3 ]; then
		echo "could not pull the pinned observability images" >&2
		exit 1
	fi
	sleep 2
done
compose up -d --wait gateway fixture tempo otel-collector prometheus grafana

printf 'Grafana is ready at http://127.0.0.1:3000 (anonymous Viewer; development only).\n'
printf 'Run ./run-smoke.sh once more to generate Agent metrics and traces.\n'
