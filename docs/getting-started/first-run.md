# First run with the v0.7.1 release

This is the shortest supported evaluation path. It uses the published gateway
and deterministic A2A fixture images, requires no provider credential, and does
not require a repository checkout or local Go toolchain.

## Prerequisites

- Docker Engine with Compose v2;
- `curl`, `tar`, and either `sha256sum` or `shasum`;
- access to GitHub Releases and GitHub Container Registry.

Download and verify the independent first-run asset:

```sh
curl -LO https://github.com/agent-guide/agent-gateway/releases/download/v0.7.1/agent-gateway-first-run-v0.7.1.tar.gz
curl -LO https://github.com/agent-guide/agent-gateway/releases/download/v0.7.1/SHA256SUMS
grep 'agent-gateway-first-run-v0.7.1.tar.gz' SHA256SUMS | shasum -a 256 -c -
tar -xzf agent-gateway-first-run-v0.7.1.tar.gz
cd agent-gateway-first-run-v0.7.1
```

On Linux, `sha256sum -c -` can replace `shasum -a 256 -c -`.

## Start and prove the path

```sh
./setup.sh
./run-smoke.sh
```

`setup.sh` prompts for an evaluation-only Admin password. It writes the
plaintext client credential as `username:password` and the server credential as
`username:<bcrypt-output>` to a mode-0600 local environment file. The published
template contains placeholders only. It then starts the gateway and fixture,
validates the included Bundle, and applies it twice to prove idempotency.

The smoke test obtains the generated VirtualKey and proves all of the following:

- an Agent turn without the VirtualKey is rejected;
- `POST /agents/first-run/turn` completes over SSE;
- the governed native A2A card and JSON-RPC path complete;
- the resulting Agent interaction is persisted;
- `agentgateway_usage_requests_total{route_kind="agent",runtime_type="http"}`
  increases.

A successful deterministic turn does not prove token accounting and does not
create a failure-counter sample. Those panels need an LLM response that reports
usage or a deliberately failed turn, respectively.

The gateway data listener is published only on `127.0.0.1:8080`. Its Admin
listener and the fixture remain on loopback inside the gateway network
namespace. SQLite data is stored in the named `gateway-data` volume.

## Inspect optional telemetry

```sh
./start-observability.sh
./run-smoke.sh
```

Open `http://127.0.0.1:3000` for the provisioned Grafana dashboard. Prometheus
shows gateway counters; Tempo supplies span duration and trace trees. The stack
uses anonymous Viewer access on host loopback and retains evaluation data for
24 hours. The observability Compose override publishes port 3000 only for this
optional path. It is not a production deployment profile.

## Stop or reset

Keep the SQLite volume:

```sh
docker compose --env-file .env.local down
```

Delete all evaluation volumes:

```sh
docker compose --env-file .env.local -f compose.yaml \
  -f compose.observability.yaml --profile observability down --volumes
```

The fixture is evaluation infrastructure, not a production Agent SDK. Continue
with the [coding-agent evaluation](../guides/evaluate-coding-agent.md) only when
an external Codex or OpenCode ACP adapter, workspace, and its upstream
authentication are available.
