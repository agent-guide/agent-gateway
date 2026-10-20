#!/bin/sh

set -eu

wait_for() {
	name=$1
	url=$2
	attempt=0
	until wget -q -T 2 -O /dev/null "$url"; do
		attempt=$((attempt + 1))
		if [ "$attempt" -ge 60 ]; then
			echo "$name did not become ready: $url" >&2
			exit 1
		fi
		sleep 1
	done
}

wait_for gateway http://127.0.0.1:8019/admin/health
wait_for fixture http://127.0.0.1:8090/healthz

agwctl validate -f /project/gateway.bundle.yaml
agwctl apply -f /project/gateway.bundle.yaml
# A second apply is part of the acceptance contract: unchanged resources must
# be skipped rather than regenerated or mutated.
agwctl apply -f /project/gateway.bundle.yaml
agwctl --output json virtualkey get first-run-key
