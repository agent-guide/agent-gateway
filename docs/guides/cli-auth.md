# CLI Auth

This guide covers the gateway's CLI-auth authenticators, how login flows create upstream credentials, and how to operate them through the Admin API or `agwctl`.

## What CLI Auth Is

CLI auth is the gateway-side mechanism for obtaining and refreshing upstream credentials through interactive login flows.

It is used for provider ecosystems where a normal static API key is not the only or best authentication path.

CLI auth integrates with:

- the `pkg/cliauth` runtime manager
- the credential manager
- the credential refresher flow

## Built-In Authenticators

Current built-in authenticators are:

- `codex`
- `claudecode`
- `gemini`

## What A Login Produces

A successful CLI-auth login produces a managed upstream credential of type:

- `cliauth_token`

The resulting credential is stored in the credential manager and can be selected by routes just like other managed credentials.

The gateway also records refresh metadata such as:

- `refresh_name`
- `refresh_expiry_delta`

## Starting A Login Through The Admin API

Current admin endpoints:

- `GET /admin/cliauth/authenticators`
- `GET /admin/cliauth/authenticators/{authenticator_name}`
- `PUT /admin/cliauth/authenticators/{authenticator_name}`
- `POST /admin/cliauth/authenticators/{authenticator_name}/login`
- `GET /admin/cliauth/logins/{login_id}`

Typical enable call:

```bash
curl -X PUT http://localhost:8019/admin/cliauth/authenticators/codex \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  --data '{"enabled":true,"config":{}}'
```

Typical login call:

```bash
curl -X POST http://localhost:8019/admin/cliauth/authenticators/codex/login \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  --data '{"provider_id":"openai-main","scope":"type:openai"}'
```

Login runs asynchronously and returns `202 Accepted`. Poll the login status endpoint until completion.

## Starting A Login With `agwctl`

Local CLI-auth login flow:

```bash
./agwctl cliauth login --authenticator codex --provider-id openai-main
```

After login, inspect gateway-stored CLI-auth credentials:

```bash
./agwctl gateway --admin-addr http://localhost:8019 \
  --admin-basic-auth admin:your-password \
  credential list \
  --type cliauth_token
```

Inspect remote authenticator and refresher status:

```bash
./agwctl gateway --admin-addr http://localhost:8019 \
  --admin-basic-auth admin:your-password \
  cliauth authenticators list
```

```bash
./agwctl gateway --admin-addr http://localhost:8019 \
  --admin-basic-auth admin:your-password \
  cliauth refresher status
```

## Authenticator Config Notes

Current common authenticator config fields include:

- `callback_port`
- `no_browser`
- `device_flow`
- `transport_profile`
- `network`

General behavior:

- authenticator config set through the Admin API is runtime-only
- disabling an authenticator or restarting the server resets it to factory defaults
- `provider_id` is required for login
- `scope` is optional but useful when you want the resulting credential to target a wider or more specific scheduling scope

Provider-specific note:

- `claudecode` supports `transport_profile: browser_like_tls`

## Recommended Scope Choices

Use `scope: id:<provider_id>` when:

- the token should only be used for one concrete provider instance

Use `scope: type:<provider_type>` when:

- the token should be shared across providers of the same type
- your route policies or managed model scopes are built around provider type grouping

## Operational Notes

- CLI-auth credentials are stored as managed credentials of type `cliauth_token`
- route default credential preference still tries `api_key` before `cliauth_token` unless the route policy changes it
- refresh behavior depends on the stored `refresh_name` and related metadata

## Related Docs

- [credentials.md](credentials.md)
- [../reference/admin-api-reference.md](../reference/admin-api-reference.md)
- [../reference/agwctl-reference.md](../reference/agwctl-reference.md)
