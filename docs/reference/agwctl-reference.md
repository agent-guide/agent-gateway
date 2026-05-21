# `agwctl` Reference

`agwctl` is the management CLI for the gateway Admin API, direct Caddy admin API operations, and local CLI auth login flows.

## Main Command Families

- `agwctl gateway ...`
- `agwctl caddy ...`
- `agwctl cliauth ...`

## Common Patterns

Show available commands:

```bash
./agwctl --help
```

List gateway LLM routes through the gateway Admin API:

```bash
./agwctl gateway --admin-addr http://localhost:8019 \
  route list \
  --admin-user admin \
  --admin-password your-password
```

List Caddy HTTP servers through the Caddy admin API directly:

```bash
./agwctl caddy --addr http://127.0.0.1:2019 server list
```

Start a local CLI auth login flow and list gateway-stored CLI auth credentials:

```bash
./agwctl cliauth login --authenticator codex --provider-id openai-main

./agwctl gateway --admin-addr http://localhost:8019 \
  --admin-user admin \
  --admin-password your-password \
  credential list \
  --type cliauth_token
```

List remote gateway CLI auth authenticators and refresher status:

```bash
./agwctl gateway --admin-addr http://localhost:8019 \
  --admin-user admin \
  --admin-password your-password \
  cliauth authenticators list
```

```bash
./agwctl gateway --admin-addr http://localhost:8019 \
  --admin-user admin \
  --admin-password your-password \
  cliauth refresher status
```

Validate a gateway bundle YAML file locally:

```bash
./agwctl gateway validate -f ./examples/gateway.bundle.minimal.yaml
```

Apply a gateway bundle YAML file through the Admin API:

```bash
./agwctl gateway --admin-addr http://localhost:8019 \
  --admin-user admin \
  --admin-password your-password \
  apply -f ./examples/gateway.bundle.minimal.yaml
```

Export remote gateway objects as bundle YAML:

```bash
./agwctl gateway --admin-addr http://localhost:8019 \
  --admin-user admin \
  --admin-password your-password \
  export -f ./gateway.bundle.yaml
```

## Important Notes

- `agwctl gateway credential ...` manages remote gateway credentials through the Admin API
- `agwctl cliauth ...` runs local login flows
- `agwctl gateway cliauth ...` inspects remote gateway CLI auth authenticators and refresher state
- `agwctl gateway apply/export ...` is the recommended CLI path for configuration objects instead of per-object JSON create or update workflows

## Related Docs

- [runtime-modes.md](runtime-modes.md)
- [admin-api-reference.md](admin-api-reference.md)
- [../design/gateway-bundle-yaml.md](../design/gateway-bundle-yaml.md)
