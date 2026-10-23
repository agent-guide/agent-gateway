#!/bin/sh

set -eu

project_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
env_file="$project_dir/.env.local"
project_name=${AGW_COMPOSE_PROJECT:-agent-gateway-first-run}

if ! docker compose version >/dev/null 2>&1; then
	echo "Docker Compose v2 is required" >&2
	exit 1
fi

password=${AGW_ADMIN_PASSWORD:-}
if [ -z "$password" ]; then
	printf 'Development Admin password: ' >&2
	stty -echo
	IFS= read -r password
	stty echo
	printf '\n' >&2
fi
case "$password" in
	''|*"'"*)
		echo "password must be non-empty and must not contain a single quote" >&2
		exit 2
		;;
esac

gateway_image=${AGW_GATEWAY_IMAGE:-ghcr.io/agent-guide/agent-gateway:v0.7.2}
hash=$(docker run --rm --read-only "$gateway_image" agw hash-password --plaintext "$password")
case "$hash" in
	\$2*) ;;
	*)
		echo "agw hash-password returned an unexpected value" >&2
		exit 1
		;;
esac

umask 077
printf "AGW_ADMIN_BASIC_AUTH='admin:%s'\nAGW_ADMIN_BASIC_AUTH_HASH='admin:%s'\n" \
	"$password" "$hash" >"$env_file"

compose() {
	docker compose --project-name "$project_name" --project-directory "$project_dir" \
		--env-file "$env_file" -f "$project_dir/compose.yaml" "$@"
}

compose up -d --wait gateway fixture
compose run --rm init
printf '\nAgent Gateway is ready at http://127.0.0.1:8080\n'
printf 'Run ./run-smoke.sh to exercise both governed Agent ingress paths.\n'
