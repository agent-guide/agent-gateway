#!/bin/sh

set -eu

admin=http://127.0.0.1:8019
data=http://127.0.0.1:8080
key_json=/tmp/first-run-key.json
turn_output=/tmp/first-run-turn.sse
native_output=/tmp/first-run-a2a.json
interaction_output=/tmp/first-run-interactions.json
metrics_output=/tmp/first-run-metrics.txt

agwctl --output json --admin-addr "$admin" virtualkey get first-run-key >"$key_json"
key=$(sed -n 's/.*"key":[[:space:]]*"\([^"]*\)".*/\1/p' "$key_json" | head -n 1)
if [ -z "$key" ]; then
	echo "generated VirtualKey was not returned" >&2
	exit 1
fi

if wget -q -T 10 -O /dev/null \
	--header 'Content-Type: application/json' \
	--post-data '{"input":"unauthenticated request"}' \
	"$data/agents/first-run/turn"; then
	echo "Agent turn unexpectedly accepted a request without a VirtualKey" >&2
	exit 1
fi

wget -q -T 30 -O "$turn_output" \
	--header 'Content-Type: application/json' \
	--header "Authorization: Bearer $key" \
	--post-data '{"input":"first-run common turn"}' \
	"$data/agents/first-run/turn"
grep -q 'event: done' "$turn_output"

wget -q -T 10 -O - "$data/a2a/first-run/.well-known/agent-card.json" \
	| grep -q '"protocolBinding":"JSONRPC"'
wget -q -T 30 -O "$native_output" \
	--header 'Content-Type: application/json' \
	--header 'A2A-Version: 1.0' \
	--header "Authorization: Bearer $key" \
	--post-data '{"jsonrpc":"2.0","id":"first-run","method":"SendMessage","params":{"message":{"messageId":"first-run-message","role":"ROLE_USER","parts":[{"text":"first-run native A2A"}]}}}' \
	"$data/a2a/first-run"
grep -q 'example-context' "$native_output"

attempt=0
until agwctl --output json --admin-addr "$admin" agent interactions first-run-agent >"$interaction_output" \
	&& grep -q 'first-run-agent' "$interaction_output"; do
	attempt=$((attempt + 1))
	if [ "$attempt" -ge 30 ]; then
		echo "first-run interaction was not persisted" >&2
		exit 1
	fi
	sleep 1
done

basic=$(printf '%s' "$AGW_ADMIN_BASIC_AUTH" | base64 | tr -d '\n')
attempt=0
until wget -q -T 5 -O "$metrics_output" \
	--header "Authorization: Basic $basic" \
	"$admin/admin/metrics/prometheus" \
	&& grep -Eq '^agentgateway_usage_requests_total\{route_kind="agent",runtime_type="http"\} [1-9][0-9]*$' "$metrics_output"; do
	attempt=$((attempt + 1))
	if [ "$attempt" -ge 30 ]; then
		echo "HTTP Agent request counter did not increase" >&2
		exit 1
	fi
	sleep 1
done

printf 'Common Agent turn, native A2A ingress, VirtualKey enforcement, interaction persistence, and request metrics passed.\n'
