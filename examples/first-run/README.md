# Provider credential free first run

This Compose project starts Agent Gateway with a deterministic development-only
A2A fixture. It proves the two governed Agent ingress paths without an LLM
provider account, API key, local Go toolchain, or external database.

Prerequisites are Docker Engine with Compose v2 and the ability to pull the two
versioned images. The gateway publishes only `127.0.0.1:8080`; its Admin API and
the fixture remain on loopback inside the shared container network namespace.

Run:

```sh
./setup.sh
./run-smoke.sh
```

`setup.sh` asks for a development-only Admin password, generates its bcrypt
form with the pinned gateway image, writes both complete credential forms to
the ignored `.env.local`, starts the services, validates the Bundle, and applies
it twice to prove idempotency. The published `.env.example` contains no usable
credential.

The smoke verifies that an unauthenticated turn is rejected, then exercises the
VirtualKey-protected common `/turn` SSE path and native A2A JSON-RPC path. It
also checks the persisted interaction and the existing
`agentgateway_usage_requests_total{route_kind="agent",runtime_type="http"}`
counter. A successful fixture turn does not prove token accounting or create a
failure counter.

Stop the services while retaining SQLite state:

```sh
docker compose --env-file .env.local down
```

Remove the services and the persisted development volume:

```sh
docker compose --env-file .env.local down --volumes
```

The fixture is evaluation infrastructure, not a production Agent SDK. The
gateway image contains no provider credential and does not include `agw-auth`.

## Optional observability profile

After the base smoke succeeds, start the non-production telemetry stack:

```sh
./start-observability.sh
./run-smoke.sh
```

Open `http://127.0.0.1:3000`. The provisioned dashboard uses only the gateway's
existing bounded Prometheus counters. Tempo supplies span duration and trace
trees; there is no Prometheus latency series. The base A2A turn proves an
Agent/HTTP request and interaction only. Token panels need an optional LLM
scenario that reports usage, failure panels need a controlled failed turn, and
model/tool children appear only when those components actually run.

Prometheus and Tempo retain evaluation data for 24 hours. Grafana uses
anonymous Viewer access on host loopback, and none of these defaults are
suitable for a shared or production deployment. Telemetry services are behind
the `observability` profile, while `compose.observability.yaml` adds the
loopback-only Grafana port only when `start-observability.sh` is used. They are
not required by the base first-run path.
