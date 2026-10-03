# Local A2A Agent

This development-only fixture serves deterministic A2A Protocol 1.0 JSON-RPC
responses for the HTTP Agent quick start and release smoke tests. It is not a
production Agent SDK or a complete A2A implementation.

Start it on loopback:

```bash
go run ./examples/a2a-agent --listen 127.0.0.1:8090
```

The endpoints are:

- `GET /healthz` — readiness;
- `GET /.well-known/agent-card.json` — Agent Card;
- `POST /a2a` — `SendMessage` and `SendStreamingMessage`.

Both methods return the stable context `example-context`; the streaming method
ends with a `TASK_STATE_COMPLETED` status update. Other JSON-RPC methods return
`-32601 Method not found`. The service is anonymous and rejects credential
headers so tests can verify that ingress VirtualKeys terminate at the gateway.

Use `--listen 127.0.0.1:0 --write-address <file>` when a harness needs an
ephemeral port and a machine-readable selected address.
